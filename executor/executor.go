package executor

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/MagalixCorp/magalix-agent/client"
	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixCorp/magalix-agent/proto"
	"github.com/MagalixCorp/magalix-agent/scanner"
	"github.com/MagalixCorp/magalix-agent/utils"
	"github.com/MagalixTechnologies/log-go"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
)

const (
	decisionsBufferLength  = 1000
	decisionsBufferTimeout = 10 * time.Second

	decisionsFeedbackExpiryTime     = 30 * time.Minute
	decisionsFeedbackExpiryCount    = 0
	decisionsFeedbackExpiryPriority = 10
	decisionsFeedbackExpiryRetries  = 5
)

// Executor decision executor
type Executor struct {
	client    *client.Client
	logger    *log.Logger
	kube      *kuber.Kube
	scanner   *scanner.Scanner
	dryRun    bool
	oomKilled chan uuid.UUID

	decisionsChan chan proto.Decision

	// TODO: remove
	changed map[uuid.UUID]struct{}
}

// InitExecutor creates a new excecutor then starts it
func InitExecutor(
	client *client.Client,
	kube *kuber.Kube,
	scanner *scanner.Scanner,
	dryRun bool,
) *Executor {
	e := NewExecutor(client, kube, scanner, dryRun)
	e.startWorker()
	return e
}

// NewExecutor creates a new excecutor
func NewExecutor(
	client *client.Client,
	kube *kuber.Kube,
	scanner *scanner.Scanner,
	dryRun bool,
) *Executor {
	executor := &Executor{
		client:  client,
		logger:  client.Logger,
		kube:    kube,
		scanner: scanner,
		dryRun:  dryRun,

		decisionsChan: make(chan proto.Decision, decisionsBufferLength),

		changed: map[uuid.UUID]struct{}{},
	}

	return executor
}

func (executor *Executor) startWorker() {
	// this method should be called one time only
	go executor.executorWorker()
}

func (executor *Executor) handleExecutionError(
	ctx *karma.Context, decision proto.Decision, err error, containerId *uuid.UUID,
) *proto.PacketDecisionFeedbackRequest {
	executor.logger.Errorf(ctx.Reason(err), "unable to execute decision")

	return &proto.PacketDecisionFeedbackRequest{
		ID:          decision.ID,
		Status:      proto.DecisionExecutionStatusFailed,
		Message:     err.Error(),
		ServiceId:   decision.ServiceId,
		ContainerId: containerId,
	}
}
func (executor *Executor) handleExecutionSkipping(
	ctx *karma.Context, decision proto.Decision, msg string,
) *proto.PacketDecisionFeedbackRequest {

	executor.logger.Infof(ctx, "skipping execution: %s", msg)

	return &proto.PacketDecisionFeedbackRequest{
		ID:        decision.ID,
		ServiceId: decision.ServiceId,
		Status:    proto.DecisionExecutionStatusSkipped,
		Message:   msg,
	}
}

func (executor *Executor) Listener(in []byte) (out []byte, err error) {
	var decisions proto.PacketDecisions
	if err = proto.Decode(in, &decisions); err != nil {
		return
	}

	for _, decision := range decisions {
		select {
		case executor.decisionsChan <- decision:
		case <-time.After(decisionsBufferTimeout):
			err := fmt.Sprintf(
				"timeout (after %s) waiting to push decision into buffer chan",
				decisionsBufferTimeout,
			)
			return proto.Encode(proto.PacketDecisionResponse{
				Error: &err,
			})
		}
	}

	return proto.Encode(proto.PacketDecisionResponse{})
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
	decision proto.Decision,
) (*proto.PacketDecisionFeedbackRequest, error) {

	ctx := karma.
		Describe("decision-id", decision.ID).
		Describe("service-id", decision.ServiceId)

	namespace, name, kind, err := executor.getServiceDetails(decision.ServiceId)
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to get service details",
		)
	}

	ctx = ctx.Describe("namespace", namespace).
		Describe("service-name", name).
		Describe("kind", kind)

	totalResources := kuber.TotalResources{
		Replicas:   decision.TotalResources.Replicas,
		Containers: make([]kuber.ContainerResourcesRequirements, 0, len(decision.TotalResources.Containers)),
	}
	for _, container := range decision.TotalResources.Containers {
		executor.changed[container.ContainerId] = struct{}{}
		containerName, err := executor.getContainerDetails(container.ContainerId)
		if err != nil {
			// TODO: do we need to soft fail here and continue execution with other containers?
			return nil, karma.Format(
				err,
				"unable to get container details",
			)
		}
		totalResources.Containers = append(totalResources.Containers, kuber.ContainerResourcesRequirements{
			Name: containerName,
			Limits: kuber.RequestLimit{
				Memory: container.Limits.Memory,
				CPU:    container.Limits.CPU,
			},
			Requests: kuber.RequestLimit{
				Memory: container.Requests.Memory,
				CPU:    container.Requests.CPU,
			},
		})
	}

	trace, _ := json.Marshal(totalResources)
	executor.logger.Debugf(
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
		msg := "decision executed successfully"

		executor.logger.Infof(ctx, msg)

		return &proto.PacketDecisionFeedbackRequest{
			ID:        decision.ID,
			ServiceId: decision.ServiceId,
			Status:    proto.DecisionExecutionStatusSucceed,
			Message:   msg,
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
