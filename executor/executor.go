package executor

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/client"
	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/scanner"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixTechnologies/log-go"
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
	decisionsExecutionTimeout		= 15 * time.Minute
	podStatusSleep					= 15 * time.Second
)

// Executor decision executor
type Executor struct {
	client        *client.Client
	logger        *log.Logger
	kube          *kuber.Kube
	scanner       *scanner.Scanner
	dryRun        bool
	oomKilled     chan uuid.UUID
	workersCount  int
	decisionsChan chan *proto.PacketDecision
	inProgressJobs   map[string]bool
}

type Replica struct {
	name string
	replicas int32
	time time.Time
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
	go e.executePendingDecisions()
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
		logger:  client.Logger,
		kube:    kube,
		scanner: scanner,
		dryRun:  dryRun,

		workersCount:  workersCount,
		inProgressJobs:   map[string]bool{},
		decisionsChan: make(chan *proto.PacketDecision, decisionsBufferLength),
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

// executePendingDecisions pulls decisions pending in execution status to execute again
// decisions can stuck in pending status if the it crashes while there are few decisions queued for execution
func (executor *Executor) executePendingDecisions() {
	decisions, err := executor.pullPendingDecisions()
	if err != nil {
		executor.logger.Errorf(
			err,
			"unable to pull due decisions",
		)
	}
	for _, decision := range decisions {
		err := executor.submitDecision(decision, decisionsPullBufferTimeout)
		if err != nil {
			executor.logger.Errorf(
				err,
				"unable to submit due decision",
			)
		}
	}
}

func (executor *Executor) pullPendingDecisions() ([]*proto.PacketDecision, error) {
	var response proto.PacketDecisionPullResponse
	err := executor.backoff(
		func() error {
			var res proto.PacketDecisionPullResponse
			err := executor.client.Send(
				proto.PacketKindDecisionPull,
				proto.PacketDecisionPullRequest{},
				&res,
			)
			if err == nil {
				response = res
			}

			return err
		},
		decisionsPullBackoffSleep,
		decisionsPullBackoffMaxRetries,
	)

	if err != nil {
		return nil, karma.Format(
			err,
			"unable to pull due decisions",
		)
	}

	return response.Decisions, nil
}

func (executor *Executor) startWorkers() {
	// this method should be called one time only
	for i := 0; i < executor.workersCount; i++ {
		go executor.executorWorker()
	}
}

func (executor *Executor) handleExecutionError(
	ctx *karma.Context, decision *proto.PacketDecision, err error, containerId *uuid.UUID,
) *proto.PacketDecisionFeedbackRequest {
	executor.logger.Errorf(ctx.Reason(err), "unable to execute decision")

	return &proto.PacketDecisionFeedbackRequest{
		ID:          decision.ID,
		Status:      proto.DecisionExecutionStatusFailed,
		Message:     err.Error(),
		ServiceId:   decision.ServiceId,
		ContainerId: decision.ContainerId,
	}
}
func (executor *Executor) handleExecutionSkipping(
	ctx *karma.Context, decision *proto.PacketDecision, msg string,
) *proto.PacketDecisionFeedbackRequest {

	executor.logger.Infof(ctx, "skipping execution: %s", msg)

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
	if decision.ContainerResources.Requests != nil && decision.ContainerResources.Requests.Memory != nil{
		*decision.ContainerResources.Requests.Memory = *decision.ContainerResources.Requests.Memory / 1024
	}
	if decision.ContainerResources.Limits != nil && decision.ContainerResources.Limits.Memory != nil{
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
			executor.logger.Errorf(
				err,
				"unable to execute decision",
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

	ctx := karma.
		Describe("decision-id", decision.ID).
		Describe("service-id", decision.ServiceId).
		Describe("container-id", decision.ContainerId)


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

	ctx = ctx.Describe("namespace", namespace).
		Describe("service-name", name).
		Describe("kind", kind)

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
				Name: container.Name,
				Limits: new(kuber.RequestLimit),
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
	executor.logger.Infof(
		ctx.
			Describe("dry run", executor.dryRun).
			Describe("cpu unit", "milliCore").
			Describe("memory unit", "mibiByte").
			Describe("trace", string(trace)),
		"executing decision",
	)

	if executor.dryRun {
		response := executor.handleExecutionSkipping(ctx, decision, "dry run enabled")
		return response, nil
	} else {
		skipped, err := executor.kube.SetResources(kind, name, namespace, totalResources)
		if err != nil {
			// TODO: do we need to retry execution before fail?
			var response *proto.PacketDecisionFeedbackRequest
			if skipped {
				response = executor.handleExecutionSkipping(ctx, decision, err.Error())
			} else {
				response = executor.handleExecutionError(ctx, decision, err, nil)
			}
			return response, nil
		}

		// short pooling to trigger pod status with max 15 minutes
		statusMap := make(map[kv1.PodPhase]string)
		statusMap[kv1.PodRunning] = "pods restarted successfully"
		statusMap[kv1.PodFailed] = "pods failed to restart"
		statusMap[kv1.PodUnknown] = "pods status is unknown"

		msg := "pods restarting exceeded timout (15 min)"
		start := time.Now()

		entitiName := ""
		result := proto.DecisionExecutionStatusFailed
		var targetPodCount int32 = 0
		var runningPods int32 = 0
		flag := false

		if strings.ToLower(kind) == "deployment"{

			replicasets, err := executor.kube.GetNamespaceReplicaSets(namespace)

			if err != nil {
				flag = true

			}else{

				currentReplicas := []Replica{}
				// get the new replicaset
				for _, replica := range replicasets.Items {
					if strings.Contains(replica.Name, name) && replica.Status.Replicas > 0{
						currentReplicas = append(currentReplicas, Replica{replica.Name, *replica.Spec.Replicas, replica.CreationTimestamp.Local()})
					}
				}

				sort.Slice(currentReplicas, func(i, j int) bool {
					return currentReplicas[i].time.After(currentReplicas[j].time)
				})

				entitiName = currentReplicas[0].name
				targetPodCount = currentReplicas[0].replicas
			}

		}else if strings.ToLower(kind) == "statefulset"{

			statefulset, err := executor.kube.GetStatefulSet(namespace, name)

			if err != nil {
				flag = true

			}else{
				// get the new StatefulSet
					if statefulset.Status.ReadyReplicas > 0 {
						entitiName = statefulset.Name
						targetPodCount = *statefulset.Spec.Replicas
					}
			}
		}

		if flag {
			msg = "failed to trigger pod status"
			result = proto.DecisionExecutionStatusFailed

		}else {

			// get pods of the new controller
			for time.Now().Sub(start) < decisionsExecutionTimeout {

				status := kv1.PodPending

				time.Sleep(podStatusSleep)
				pods, err := executor.kube.GetNameSpacePods(namespace)

				if err != nil {
					msg = "failed to trigger pod status"
					result = proto.DecisionExecutionStatusFailed
					break
				}

				for _, pod := range pods.Items {
					if strings.Contains(pod.Name, entitiName){
						executor.logger.Info(pod.Name, ", status: ", pod.Status.Phase)
						status = pod.Status.Phase
						if status == kv1.PodRunning {
							runningPods++
						}else if status != kv1.PodPending {
							break
						}
					}
				}

				if runningPods == targetPodCount {
					msg = statusMap[status]
					result = proto.DecisionExecutionStatusSucceed
					break
				}
			}
		}

		//rollback in case of failed to restart all pods
		if runningPods < targetPodCount {

			msg = statusMap[kv1.PodFailed]
			result = proto.DecisionExecutionStatusFailed

			memoryLimit := container.Resources.Limits.Memory().Value()
			memoryRequest := container.Resources.Requests.Memory().Value()
			cpuLimit := container.Resources.Limits.Cpu().MilliValue()
			cpuRequest := container.Resources.Requests.Cpu().MilliValue()

			*totalResources.Containers[0].Limits.Memory = memoryLimit / 1024 / 1024
			*totalResources.Containers[0].Requests.Memory = memoryRequest /1024 / 1024
			*totalResources.Containers[0].Limits.CPU = cpuLimit
			*totalResources.Containers[0].Requests.CPU = cpuRequest

			// execute the decision with old values to rollback
			_, err := executor.kube.SetResources(kind, name, namespace, totalResources)

			if err != nil {
				executor.logger.Warning(ctx, "can't rollback decision")
			}
		}

		executor.logger.Infof(ctx, msg)

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
