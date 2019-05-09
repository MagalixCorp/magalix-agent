package executor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MagalixCorp/magalix-agent/client"
	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixCorp/magalix-agent/proto"
	"github.com/MagalixCorp/magalix-agent/scanner"
	"github.com/MagalixTechnologies/log-go"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
	"k8s.io/api/apps/v1"
)

// Executor decision executor
type Executor struct {
	client    *client.Client
	logger    *log.Logger
	kube      *kuber.Kube
	scanner   *scanner.Scanner
	dryRun    bool
	oomKilled chan uuid.UUID

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
	return NewExecutor(client, kube, scanner, dryRun)
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

		changed: map[uuid.UUID]struct{}{},
	}

	return executor
}

func (executor *Executor) handleExecutionError(
	ctx *karma.Context, decision proto.Decision, err error, containerId *uuid.UUID,
) *proto.DecisionExecutionResponse {
	executor.logger.Errorf(ctx.Reason(err), "unable to execute decision")

	return &proto.DecisionExecutionResponse{
		ID:          decision.ID,
		Status:      proto.DecisionExecutionStatusFailed,
		Message:     err.Error(),
		ServiceId:   decision.ServiceId,
		ContainerId: containerId,
	}
}
func (executor *Executor) handleExecutionSkipping(
	ctx *karma.Context, decision proto.Decision, msg string,
) *proto.DecisionExecutionResponse {

	executor.logger.Infof(ctx, "skipping execution: %s", msg)

	return &proto.DecisionExecutionResponse{
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

	var responses proto.PacketDecisionsResponse
	for _, decision := range decisions {
		ctx := karma.
			Describe("decision-id", decision.ID).
			Describe("service-id", decision.ServiceId)

		namespace, name, kind, err := executor.getServiceDetails(decision.ServiceId)
		if err != nil {
			response := executor.handleExecutionError(ctx, decision, err, nil)
			responses = append(responses, *response)
			continue
		}

		ctx = ctx.Describe("namespace", namespace).
			Describe("service-name", name).
			Describe("kind", kind)

		if strings.ToLower(kind) == "statefulset" {
			statefulSet, err := executor.kube.GetStatefulSet(namespace, name)
			if err != nil {
				response := executor.handleExecutionError(ctx, decision, err, nil)
				responses = append(responses, *response)
				continue
			}

			ctx = ctx.
				Describe("replicas", statefulSet.Spec.Replicas)

			if statefulSet.Spec.Replicas != nil && *statefulSet.Spec.Replicas > 1 {
				msg := fmt.Sprintf("sts replicas %v > 1", statefulSet.Spec.Replicas)

				updateStrategy := statefulSet.Spec.UpdateStrategy.Type
				ctx = ctx.
					Describe("update-strategy", updateStrategy)

				if updateStrategy == v1.RollingUpdateStatefulSetStrategyType {

					// no rollingUpdate spec, then Partition = 0
					if statefulSet.Spec.UpdateStrategy.RollingUpdate != nil {
						partition := statefulSet.Spec.UpdateStrategy.RollingUpdate.Partition
						ctx = ctx.
							Describe("rolling-update-partition", partition)

						if partition != nil && *partition != 0 {
							response := executor.handleExecutionSkipping(
								ctx, decision,
								msg+" and Spec.UpdateStrategy.RollingUpdate.Partition not equal 0",
							)
							responses = append(responses, *response)
							continue
						}
					}

				} else {
					response := executor.handleExecutionSkipping(
						ctx, decision,
						msg+" and Spec.UpdateStrategy not equal 'RollingUpdate'",
					)
					responses = append(responses, *response)
					continue
				}
			}
		}

		totalResources := kuber.TotalResources{
			Replicas:   decision.TotalResources.Replicas,
			Containers: make([]kuber.ContainerResourcesRequirements, 0, len(decision.TotalResources.Containers)),
		}
		for _, container := range decision.TotalResources.Containers {
			executor.changed[container.ContainerId] = struct{}{}
			containerName, err := executor.getContainerDetails(container.ContainerId)
			if err != nil {
				containerCtx := ctx.Describe("container-name", containerName)
				response := executor.handleExecutionError(containerCtx, decision, err, &container.ContainerId)
				responses = append(responses, *response)
				continue
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
			responses = append(responses, *response)
			continue
		} else {
			err = executor.kube.SetResources(kind, name, namespace, totalResources)
			if err != nil {
				response := executor.handleExecutionError(ctx, decision, err, nil)
				responses = append(responses, *response)
				continue
			}
			msg := "decision executed successfully"

			executor.logger.Infof(ctx, msg)

			responses = append(responses, proto.DecisionExecutionResponse{
				ID:        decision.ID,
				ServiceId: decision.ServiceId,
				Status:    proto.DecisionExecutionStatusSucceed,
				Message:   msg,
			})
		}

	}

	return proto.Encode(responses)
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
