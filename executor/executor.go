package executor

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/client"
	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	kv1 "k8s.io/api/core/v1"
)

const (
	decisionsBufferLength  = 1000
	decisionsBufferTimeout = 10 * time.Second

	decisionsFeedbackExpiryTime     = 30 * time.Minute
	decisionsFeedbackExpiryCount    = 0
	decisionsFeedbackExpiryPriority = 10
	decisionsFeedbackExpiryRetries  = 5
	decisionsExecutionTimeout       = 15 * time.Minute
	podStatusSleep                  = 15 * time.Second
)

type EntitiesFinder interface {
	FindController(namespaceName string, controllerKind string, controllerName string) (*unstructured.Unstructured, error)
	FindContainer(namespaceName string, controllerKind string, controllerName string, containerName string) (*kv1.Container, error)
}

// Executor decision executor
type Executor struct {
	client          *client.Client
	logger          *log.Logger
	kube            *kuber.Kube
	entitiesFinder  EntitiesFinder
	dryRun          bool
	workersCount    int
	automationsChan chan *proto.PacketAutomation
	inProgressJobs  map[string]bool
}

type Replica struct {
	name     string
	replicas int32
	time     time.Time
}

// InitExecutor creates a new executor then starts it
func InitExecutor(
	client *client.Client,
	kube *kuber.Kube,
	entitiesFinder EntitiesFinder,
	workersCount int,
	dryRun bool,
) *Executor {
	e := NewExecutor(client, kube, entitiesFinder, workersCount, dryRun)
	e.startWorkers()
	return e
}

// NewExecutor creates a new executor
func NewExecutor(
	client *client.Client,
	kube *kuber.Kube,
	entitiesFinder EntitiesFinder,
	workersCount int,
	dryRun bool,
) *Executor {
	executor := &Executor{
		client:         client,
		logger:         client.Logger,
		kube:           kube,
		entitiesFinder: entitiesFinder,
		dryRun:         dryRun,

		workersCount:    workersCount,
		inProgressJobs:  map[string]bool{},
		automationsChan: make(chan *proto.PacketAutomation, decisionsBufferLength),
	}

	return executor
}

func (executor *Executor) backoff(
	fn func() error, sleep time.Duration, maxRetries int,
) error {
	return utils.WithBackoff(
		fn,
		utils.Backoff{
			Sleep:      sleep,
			MaxRetries: maxRetries,
		},
		executor.logger,
	)
}

func (executor *Executor) startWorkers() {
	// this method should be called one time only
	for i := 0; i < executor.workersCount; i++ {
		go executor.executorWorker()
	}
}

func (executor *Executor) handleExecutionError(
	ctx *karma.Context, automation *proto.PacketAutomation, err error,
) *proto.PacketAutomationFeedbackRequest {
	executor.logger.Errorf(ctx.Reason(err), "unable to execute decision")

	return makeAutomationFailedResponse(automation, err.Error())
}
func (executor *Executor) handleExecutionSkipping(
	ctx *karma.Context, aut *proto.PacketAutomation, msg string,
) *proto.PacketAutomationFeedbackRequest {

	executor.logger.Infof(ctx, "skipping execution: %s", msg)

	return makeAutomationFailedResponse(aut, msg)
}

func (executor *Executor) Listener(in []byte) (out []byte, err error) {

	var automation proto.PacketAutomation
	if err = proto.DecodeSnappy(in, &automation); err != nil {
		return
	}
	_, exist := executor.inProgressJobs[automation.ID.String()]
	if !exist {
		executor.inProgressJobs[automation.ID.String()] = true
		convertDecisionMemoryFromKiloByteToMegabyte(&automation)

		err = executor.submitAutomation(&automation, decisionsBufferTimeout)
		if err != nil {
			errMessage := err.Error()
			return proto.EncodeSnappy(proto.PacketAutomationResponse{
				Error: &errMessage,
			})
		}
	}

	return proto.EncodeSnappy(proto.PacketAutomationResponse{})
}

func convertDecisionMemoryFromKiloByteToMegabyte(automation *proto.PacketAutomation) {
	if automation.ContainerResources.Requests != nil && automation.ContainerResources.Requests.Memory != nil {
		*automation.ContainerResources.Requests.Memory = *automation.ContainerResources.Requests.Memory / 1024
	}
	if automation.ContainerResources.Limits != nil && automation.ContainerResources.Limits.Memory != nil {
		*automation.ContainerResources.Limits.Memory = *automation.ContainerResources.Limits.Memory / 1024
	}
}

func (executor *Executor) submitAutomation(
	automation *proto.PacketAutomation,
	timeout time.Duration,
) error {
	select {
	case executor.automationsChan <- automation:
	case <-time.After(timeout):
		return fmt.Errorf(
			"timeout (after %s) waiting to push decision into buffer chan",
			decisionsBufferTimeout,
		)
	}
	return nil
}

func (executor *Executor) executorWorker() {
	for automation := range executor.automationsChan {
		// TODO: execute decisions in batches
		response, err := executor.execute(automation)
		if err != nil {
			executor.logger.Errorf(
				err,
				"unable to execute decision",
			)
		}

		delete(executor.inProgressJobs, automation.ID.String())

		executor.client.Pipe(
			client.Package{
				Kind:        proto.PacketKindAutomationFeedback,
				ExpiryTime:  utils.After(decisionsFeedbackExpiryTime),
				ExpiryCount: decisionsFeedbackExpiryCount,
				Priority:    decisionsFeedbackExpiryPriority,
				Retries:     decisionsFeedbackExpiryRetries,
				Data:        response,
			},
		)
	}
}

func (executor *Executor) execute(
	automation *proto.PacketAutomation,
) (*proto.PacketAutomationFeedbackRequest, error) {

	ctx := karma.
		Describe("decision-id", automation.ID).
		Describe("namespace-name", automation.NamespaceName).
		Describe("controller-name", automation.ControllerName).
		Describe("controller-kind", automation.ControllerKind).
		Describe("container-name", automation.ContainerName)

	c, err := executor.entitiesFinder.FindContainer(
		automation.NamespaceName,
		automation.ControllerKind,
		automation.ControllerName,
		automation.ContainerName,
	)
	if err != nil {
		return makeAutomationFailedResponse(automation, fmt.Sprintf("unable to get container details %s", err.Error())),
			karma.Format(err, "unable to get container details")
	}

	var container kv1.Container
	err = utils.Transcode(c, container)
	if err != nil {
		return makeAutomationFailedResponse(automation, fmt.Sprintf("unable to get container details %s", err.Error())),
			karma.Format(err, "unable to get container details")
	}

	totalResources := kuber.TotalResources{
		Containers: []kuber.ContainerResourcesRequirements{
			{
				Name:     container.Name,
				Limits:   new(kuber.RequestLimit),
				Requests: new(kuber.RequestLimit),
			},
		},
	}
	if automation.ContainerResources.Requests != nil {
		if automation.ContainerResources.Requests.CPU != nil {
			totalResources.Containers[0].Requests.CPU = automation.ContainerResources.Requests.CPU
		}
		if automation.ContainerResources.Requests.Memory != nil {
			totalResources.Containers[0].Requests.Memory = automation.ContainerResources.Requests.Memory
		}
	}
	if automation.ContainerResources.Limits != nil {
		if automation.ContainerResources.Limits.CPU != nil {
			totalResources.Containers[0].Limits.CPU = automation.ContainerResources.Limits.CPU
		}
		if automation.ContainerResources.Limits.Memory != nil {
			totalResources.Containers[0].Limits.Memory = automation.ContainerResources.Limits.Memory
		}
	}

	trace, _ := json.Marshal(totalResources)
	executor.logger.Infof(
		ctx.
			Describe("ClusterID", executor.client.ClusterID).
			Describe("AccountID", executor.client.AccountID).
			Describe("dry run", executor.dryRun).
			Describe("cpu unit", "milliCore").
			Describe("memory unit", "mibiByte").
			Describe("trace", string(trace)),
		"executing decision",
	)

	if executor.dryRun {
		response := executor.handleExecutionSkipping(ctx, automation, "dry run enabled")
		return response, nil
	} else {
		skipped, err := executor.kube.SetResources(
			automation.ControllerKind,
			automation.ControllerName,
			automation.NamespaceName,
			totalResources,
		)
		if err != nil {
			// TODO: do we need to retry execution before fail?
			var response *proto.PacketAutomationFeedbackRequest
			if skipped {
				response = executor.handleExecutionSkipping(ctx, automation, err.Error())
			} else {
				response = executor.handleExecutionError(ctx, automation, err)
			}
			return response, nil
		}

		// short pooling to trigger pod status with max 15 minutes
		statusMap := make(map[kv1.PodPhase]string)
		statusMap[kv1.PodRunning] = "pods restarted successfully"
		statusMap[kv1.PodFailed] = "pods failed to restart"
		statusMap[kv1.PodUnknown] = "pods status is unknown"

		result, msg, targetPodCount, runningPods := executor.podsStatusHandler(
			automation.ControllerName,
			automation.NamespaceName,
			automation.ControllerKind,
			statusMap,
		)

		//rollback in case of failed to restart all pods
		if runningPods < targetPodCount {

			msg = statusMap[kv1.PodFailed]
			result = proto.AutomationFailed
			memoryLimit := container.Resources.Limits.Memory().Value()
			memoryRequest := container.Resources.Requests.Memory().Value()
			cpuLimit := container.Resources.Limits.Cpu().MilliValue()
			cpuRequest := container.Resources.Requests.Cpu().MilliValue()

			// handle if requests and limits is null in rollback DEV-2056"
			if container.Resources.Limits != nil {
				if container.Resources.Limits.Cpu() != nil && cpuLimit != 0 {
					*totalResources.Containers[0].Limits.CPU = cpuLimit
				}
				if container.Resources.Limits.Memory() != nil && memoryLimit != 0 {
					*totalResources.Containers[0].Limits.Memory = memoryLimit / 1024 / 1024
				}
			}

			if container.Resources.Requests != nil {
				if container.Resources.Requests.Cpu() != nil && cpuRequest != 0 {
					*totalResources.Containers[0].Requests.CPU = cpuRequest
				}
				if container.Resources.Requests.Memory() != nil && memoryRequest != 0 {
					*totalResources.Containers[0].Requests.Memory = memoryRequest / 1024 / 1024
				}
			}

			// execute the decision with old values to rollback
			_, err := executor.kube.SetResources(
				automation.ControllerKind,
				automation.ControllerName,
				automation.NamespaceName,
				totalResources,
			)

			if err != nil {
				executor.logger.Warning(ctx, "can't rollback decision")
			}
		}

		executor.logger.Infof(ctx, msg)

		return &proto.PacketAutomationFeedbackRequest{
			ID:             automation.ID,
			NamespaceName:  automation.NamespaceName,
			ControllerName: automation.ControllerName,
			ControllerKind: automation.ControllerKind,
			ContainerName:  automation.ContainerName,
			Status:         result,
			Message:        msg,
		}, nil
	}

}

func makeAutomationFailedResponse(automation *proto.PacketAutomation, msg string) *proto.PacketAutomationFeedbackRequest {
	return &proto.PacketAutomationFeedbackRequest{
		ID:             automation.ID,
		NamespaceName:  automation.NamespaceName,
		ControllerKind: automation.ControllerName,
		ControllerName: automation.ControllerName,
		ContainerName:  automation.ContainerName,
		Status:         proto.AutomationFailed,
		Message:        msg,
	}
}
