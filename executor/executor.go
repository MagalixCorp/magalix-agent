package executor

import (
	"encoding/json"
	"fmt"
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

// InitExecutor creates a new excecutor then starts it
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

// NewExecutor creates a new excecutor
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
	*decision.ContainerResources.Requests.Memory = *decision.ContainerResources.Requests.Memory / 1024
	*decision.ContainerResources.Limits.Memory = *decision.ContainerResources.Limits.Memory / 1024
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

	containerName, err := executor.getContainerDetails(decision.ContainerId)
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
				Name: containerName,
				Limits: kuber.RequestLimit{
					Memory: decision.ContainerResources.Limits.Memory,
					CPU:    decision.ContainerResources.Limits.CPU,
				},
				Requests: kuber.RequestLimit{
					Memory: decision.ContainerResources.Requests.Memory,
					CPU:    decision.ContainerResources.Requests.CPU,
				},
			},
		},
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

		statusMap := make(map[string]string)
		statusMap["Running"] = "pod restarted successfully"
		statusMap["Failed"] = "pod failed to restart"
		statusMap["Unknown"] = "pod status is unknown"
		statusMap["PodInitializing"] = "pod restarting"

		backoff := 15 * time.Minute
		msg := "pod restarting exceeded timout (15 min)"
		start := time.Now()
		timeout := int(backoff.Seconds())

		for time.Now().Second() - start.Second() < timeout {

			status := "Pending"

			time.Sleep(15 * time.Second)
			pods, _ := executor.kube.GetNameSpacePods(namespace)

			for _, pod := range pods.Items {
				if strings.Contains(pod.Name, name){
					executor.logger.Info(pod.Name, ", status: ", pod.Status.Phase)
					status = string(pod.Status.Phase)
					break
				}
			}

			if status != "Pending" {
				msg = statusMap[status]
				break
			}

		}

		executor.logger.Infof(ctx, msg, time.Now().Second(), ": ", start.Second())

		return &proto.PacketDecisionFeedbackRequest{
			ID:          decision.ID,
			ServiceId:   decision.ServiceId,
			ContainerId: decision.ContainerId,
			Status:      proto.DecisionExecutionStatusSucceed,
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

func (executor *Executor) getContainerDetails(containerID uuid.UUID) (name string, err error) {
	name, ok := executor.scanner.FindContainerNameByID(executor.scanner.GetApplications(), containerID)
	if !ok {
		err = karma.Describe("id", containerID).
			Reason("container not found")
	}
	return
}
