package executor

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/client"
	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/scanner"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
	kv1 "k8s.io/api/core/v1"
)

const (
	decisionsBufferLength  = 1000
	decisionsBufferTimeout = 10 * time.Second

	decisionsPullBufferTimeout     = 2 * time.Minute
	decisionsPullBackoffSleep      = 1 * time.Second
	decisionsPullBackoffMaxRetries = 10

	decisionsFeedbackExpiryTime     = 30 * time.Minute
	decisionsFeedbackExpiryCount    = 0
	decisionsFeedbackExpiryPriority = 10
	decisionsFeedbackExpiryRetries  = 5
	decisionsExecutionTimeout       = 15 * time.Minute
	podStatusSleep                  = 15 * time.Second
)

// Executor decision executor
type Executor struct {
	client         *client.Client
	kube           *kuber.Kube
	scanner        *scanner.Scanner
	dryRun         bool
	workersCount   int
	decisionsChan  chan *proto.PacketDecision
	inProgressJobs map[string]bool
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
	scanner *scanner.Scanner,
	workersCount int,
	dryRun bool,
) *Executor {
	e := NewExecutor(client, kube, scanner, workersCount, dryRun)
	e.startWorkers()
	return e
}

// NewExecutor creates a new executor
func NewExecutor(
	client *client.Client,
	kube *kuber.Kube,
	scanner *scanner.Scanner,
	workersCount int,
	dryRun bool,
) *Executor {
	executor := &Executor{
		client:  client,
		kube:    kube,
		scanner: scanner,
		dryRun:  dryRun,

		workersCount:   workersCount,
		inProgressJobs: map[string]bool{},
		decisionsChan:  make(chan *proto.PacketDecision, decisionsBufferLength),
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
	)
}

func (executor *Executor) startWorkers() {
	// this method should be called one time only
	for i := 0; i < executor.workersCount; i++ {
		go executor.executorWorker()
	}
}

func (executor *Executor) handleExecutionError(
	decision *proto.PacketDecision, err error, containerId *uuid.UUID,
) *proto.PacketDecisionFeedbackRequest {
	logger.Errorw("unable to execute decision", "error", err, "decisionId", decision.ID)

	return &proto.PacketDecisionFeedbackRequest{
		ID:          decision.ID,
		Status:      proto.DecisionExecutionStatusFailed,
		Message:     err.Error(),
		ServiceId:   decision.ServiceId,
		ContainerId: decision.ContainerId,
	}
}
func (executor *Executor) handleExecutionSkipping(
	decision *proto.PacketDecision, msg string,
) *proto.PacketDecisionFeedbackRequest {

	logger.Debugw("skipping decision execution", "msg", msg, "decisionId", decision.ID)

	return &proto.PacketDecisionFeedbackRequest{
		ID:        decision.ID,
		ServiceId: decision.ServiceId,
		Status:    proto.DecisionExecutionStatusFailed,
		Message:   msg,
	}
}

func (executor *Executor) Listener(in []byte) (out []byte, err error) {

	var decision proto.PacketDecision
	if err = proto.DecodeSnappy(in, &decision); err != nil {
		return
	}
	_, exist := executor.inProgressJobs[decision.ID.String()]
	if !exist {
		executor.inProgressJobs[decision.ID.String()] = true
		convertDecisionMemoryFromKiloByteToMegabyte(&decision)

		err = executor.submitDecision(&decision, decisionsBufferTimeout)
		if err != nil {
			errMessage := err.Error()
			return proto.EncodeSnappy(proto.PacketDecisionResponse{
				Error: &errMessage,
			})
		}
	}

	return proto.EncodeSnappy(proto.PacketDecisionResponse{})
}

func convertDecisionMemoryFromKiloByteToMegabyte(decision *proto.PacketDecision) {
	if decision.ContainerResources.Requests != nil && decision.ContainerResources.Requests.Memory != nil {
		*decision.ContainerResources.Requests.Memory = *decision.ContainerResources.Requests.Memory / 1024
	}
	if decision.ContainerResources.Limits != nil && decision.ContainerResources.Limits.Memory != nil {
		*decision.ContainerResources.Limits.Memory = *decision.ContainerResources.Limits.Memory / 1024
	}
}

func (executor *Executor) submitDecision(
	decision *proto.PacketDecision,
	timeout time.Duration,
) error {
	select {
	case executor.decisionsChan <- decision:
	case <-time.After(timeout):
		return fmt.Errorf(
			"timeout (after %s) waiting to push decision into buffer chan",
			decisionsBufferTimeout,
		)
	}
	return nil
}

func (executor *Executor) executorWorker() {
	for decision := range executor.decisionsChan {
		// TODO: execute decisions in batches
		response, err := executor.execute(decision)
		if err != nil {
			logger.Errorw(
				"unable to execute decision",
				"error", err,
				"decisionId", decision.ID,
			)
		}

		delete(executor.inProgressJobs, decision.ID.String())

		executor.client.Pipe(
			client.Package{
				Kind:        proto.PacketKindDecisionFeedback,
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
	decision *proto.PacketDecision,
) (*proto.PacketDecisionFeedbackRequest, error) {

	namespace, name, kind, err := executor.getServiceDetails(decision.ServiceId)
	if err != nil {
		return &proto.PacketDecisionFeedbackRequest{
				ID:        decision.ID,
				ServiceId: decision.ServiceId,
				Status:    proto.DecisionExecutionStatusFailed,
				Message:   "unable to get service details",
			}, karma.Format(
				err,
				"unable to get service details",
			)
	}

	lg := logger.With(
		"decision-id", decision.ID,
		"service-id", decision.ServiceId,
		"container-id", decision.ContainerId,
		"service-name", name,
		"kind", kind,
	)

	container, err := executor.getContainerDetails(decision.ContainerId)
	if err != nil {
		return &proto.PacketDecisionFeedbackRequest{
				ID:        decision.ID,
				ServiceId: decision.ServiceId,
				Status:    proto.DecisionExecutionStatusFailed,
				Message:   "unable to get container details",
			}, karma.Format(
				err,
				"unable to get container details",
			)
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
	if decision.ContainerResources.Requests != nil {
		if decision.ContainerResources.Requests.CPU != nil {
			totalResources.Containers[0].Requests.CPU = decision.ContainerResources.Requests.CPU
		}
		if decision.ContainerResources.Requests.Memory != nil {
			totalResources.Containers[0].Requests.Memory = decision.ContainerResources.Requests.Memory
		}
	}
	if decision.ContainerResources.Limits != nil {
		if decision.ContainerResources.Limits.CPU != nil {
			totalResources.Containers[0].Limits.CPU = decision.ContainerResources.Limits.CPU
		}
		if decision.ContainerResources.Limits.Memory != nil {
			totalResources.Containers[0].Limits.Memory = decision.ContainerResources.Limits.Memory
		}
	}

	trace, _ := json.Marshal(totalResources)
	lg.Debugw(
		"executing decision",
		"dry run", executor.dryRun,
		"cpu unit", "milliCore",
		"memory unit", "mibiByte",
		"trace", string(trace),
	)

	if executor.dryRun {
		response := executor.handleExecutionSkipping(decision, "dry run enabled")
		return response, nil
	} else {
		skipped, err := executor.kube.SetResources(kind, name, namespace, totalResources)
		if err != nil {
			// TODO: do we need to retry execution before fail?
			var response *proto.PacketDecisionFeedbackRequest
			if skipped {
				response = executor.handleExecutionSkipping(decision, err.Error())
			} else {
				response = executor.handleExecutionError(decision, err, nil)
			}
			return response, nil
		}

		// short pooling to trigger pod status with max 15 minutes
		statusMap := make(map[kv1.PodPhase]string)
		statusMap[kv1.PodRunning] = "pods restarted successfully"
		statusMap[kv1.PodFailed] = "pods failed to restart"
		statusMap[kv1.PodUnknown] = "pods status is unknown"

		result, msg, targetPodCount, runningPods := executor.podsStatusHandler(name, namespace, kind, statusMap)

		//rollback in case of failed to restart all pods
		if runningPods < targetPodCount {

			msg = statusMap[kv1.PodFailed]
			result = proto.DecisionExecutionStatusFailed
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
			_, err := executor.kube.SetResources(kind, name, namespace, totalResources)

			if err != nil {
				lg.Warn("can't rollback decision")
			}
		}

		lg.Debug(msg)

		return &proto.PacketDecisionFeedbackRequest{
			ID:          decision.ID,
			ServiceId:   decision.ServiceId,
			ContainerId: decision.ContainerId,
			Status:      result,
			Message:     msg,
		}, nil
	}

}

func (executor *Executor) getServiceDetails(serviceID uuid.UUID) (namespace, name, kind string, err error) {
	namespace, name, kind, ok := executor.scanner.FindServiceByID(executor.scanner.GetApplications(), serviceID)
	if !ok {
		err = karma.Describe("id", serviceID).
			Reason("service not found")
	}
	return
}

func (executor *Executor) getContainerDetails(containerID uuid.UUID) (container *scanner.Container, err error) {
	container, ok := executor.scanner.FindContainerByID(executor.scanner.GetApplications(), containerID)
	if !ok {
		err = karma.Describe("id", containerID).
			Reason("container not found")
	}
	return
}
