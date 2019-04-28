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
	oomKilled chan uuid.UUID,
	args map[string]interface{},
) *Executor {
	executor := NewExecutor(client, kube, scanner, args["--dry-run"].(bool), oomKilled)
	executor.Init()
	return executor
}

// NewExecutor creates a new excecutor
func NewExecutor(
	client *client.Client,
	kube *kuber.Kube,
	scanner *scanner.Scanner,
	dryRun bool,
	oomKilled chan uuid.UUID,
) *Executor {
	executor := &Executor{
		client:    client,
		logger:    client.Logger,
		kube:      kube,
		scanner:   scanner,
		dryRun:    dryRun,
		oomKilled: oomKilled,

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
		ContainerId: containerId,
	}
}
func (executor *Executor) handleExecutionSkipping(
	ctx *karma.Context, decision proto.Decision, msg string,
) *proto.DecisionExecutionResponse {

	executor.logger.Infof(ctx, "skipping execution: %s", msg)

	return &proto.DecisionExecutionResponse{
		ID:      decision.ID,
		Status:  proto.DecisionExecutionStatusSkipped,
		Message: msg,
	}
}

func (executor *Executor) Listener(in []byte) (out []byte, err error) {
	var decisions proto.PacketDecisions
	if err = proto.Decode(in, &decisions); err != nil {
		return
	}

	var responses proto.PacketDecisionsResponse
	for _, decision := range decisions {
		ctx := karma.Describe("decision-id", decision.ID)

		namespace, name, kind, err := executor.getServiceDetails(decision.ID)
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
			executor.changed[container.ID] = struct{}{}
			containerName, err := executor.getContainerDetails(container.ID)
			if err != nil {
				containerCtx := ctx.Describe("container-name", containerName)
				response := executor.handleExecutionError(containerCtx, decision, err, &container.ID)
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
				ID:      decision.ID,
				Status:  proto.DecisionExecutionStatusSucceed,
				Message: msg,
			})
		}

	}

	return proto.Encode(responses)
}

// Init adds decision listener and starts oom watcher
func (executor *Executor) Init() {
	go executor.watchOOM()
}

func (executor *Executor) watchOOM() {
	stopped := false
	scanner := executor.scanner.WaitForNextTick()
	// a set of uuids
	uuids := make(map[uuid.UUID]struct{})
	for !stopped {
		select {
		case oomKilled := <-executor.oomKilled:
			uuids[oomKilled] = struct{}{}
		case <-scanner:
			scanner = executor.scanner.WaitForNextTick()
			for oomKilled := range uuids {
				uuid := oomKilled
				if container, service, application, ok := executor.scanner.FindContainerByID(
					executor.scanner.GetApplications(), uuid,
				); ok {
					newValue := container.Resources.Limits.Memory().Value() * 3 / 2 / 1024 / 1024
					executor.logger.Infof(karma.
						Describe("conatainer", container.Name).
						Describe("service", service.Name).
						Describe("application", application.Name).
						Describe("old value", container.Resources.Limits.Memory()).
						Describe("new value (Mi)", newValue).
						Describe("dry run", executor.dryRun),
						"executing oom handler",
					)
					if !executor.dryRun {
						// TODO: remove
						// skip if not changed
						if _, ok := executor.changed[uuid]; !ok {
							continue
						}
						cpuLimits := container.Resources.Limits.Cpu().MilliValue()

						cpuRequests := container.Resources.Requests.Cpu().MilliValue()
						memoryRequests := container.Resources.Requests.Memory().Value() / 1024 / 1024

						err := executor.kube.SetResources(service.Kind, service.Name, application.Name, kuber.TotalResources{
							Containers: []kuber.ContainerResourcesRequirements{kuber.ContainerResourcesRequirements{
								Name: container.Name,
								Limits: kuber.RequestLimit{
									Memory: &newValue,
									CPU:    &cpuLimits,
								},
								Requests: kuber.RequestLimit{
									Memory: &memoryRequests,
									CPU:    &cpuRequests,
								},
							}},
						})
						if err != nil {
							executor.logger.Errorf(err, "unable to execute oom hadnler")
						} else {
							executor.logger.Infof(karma.
								Describe("conatainer", container.Name).
								Describe("service", service.Name).
								Describe("application", application.Name).
								Describe("old value", container.Resources.Limits.Memory()).
								Describe("new value", newValue).
								Describe("dry run", executor.dryRun),
								"oom handler executed",
							)
						}
					}
				}
			}
			uuids = make(map[uuid.UUID]struct{})
		}

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
