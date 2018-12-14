package executor

import (
	"encoding/json"
	"fmt"
	"github.com/MagalixCorp/magalix-agent/kuber"

	"github.com/MagalixCorp/magalix-agent/client"
	"github.com/MagalixCorp/magalix-agent/proto"
	"github.com/MagalixCorp/magalix-agent/scanner"
	"github.com/MagalixTechnologies/log-go"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
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

func (executor *Executor) listener(in []byte) (out []byte, err error) {
	var decisions proto.PacketDecisions
	if err = proto.Decode(in, &decisions); err != nil {
		return
	}

	failed := 0

	var errs []error
	for _, decision := range decisions {
		var err error
		namespace, name, kind, err := executor.getServiceDetails(decision.ID)
		if err != nil {
			errs = append(errs, err)
			goto err
		}
		{
			totalResources := kuber.TotalResources{
				Replicas:   decision.TotalResources.Replicas,
				Containers: make([]kuber.ContainerResourcesRequirements, 0, len(decision.TotalResources.Containers)),
			}
			for _, container := range decision.TotalResources.Containers {
				executor.changed[container.ID] = struct{}{}
				containerName, err := executor.getContainerDetails(container.ID)
				if err != nil {
					errs = append(errs, err)
					goto err
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
			{
				// TODO: debug
				trace, _ := json.Marshal(totalResources)
				msg := "decision executed"
				if executor.dryRun {
					msg += " (dry run)"
				}
				executor.logger.Infof(
					karma.
						Describe("dry run", executor.dryRun).
						Describe("cpu unit", "milliCore").
						Describe("memory unit", "mibiByte").
						Describe("decision", string(trace)),
					msg,
				)
			}
			if !executor.dryRun {
				err = executor.kube.SetResources(kind, name, namespace, totalResources)
				if err != nil {
					errs = append(errs, err)
					goto err
				}
			}
		}
		continue
	err:
		failed++
	}
	if len(errs) > 0 {
		executor.logger.Errorf(errs[0], "unable execute decisions %#+v", decisions)
		err = fmt.Errorf("unable to execute some of the decisions: %s", errs)
	}
	executor.logger.Infof(karma.Describe("decisions", decisions), "decisions executed")
	resp := proto.PacketDecisionsResponse{
		Executed: len(decisions) - failed,
		Failed:   failed,
	}

	out, encodeErr := proto.Encode(resp)
	if err == nil {
		err = encodeErr
	}
	return
}

// Init adds decision listener and starts oom watcher
func (executor *Executor) Init() {
	executor.client.AddListener(proto.PacketKindDecision, executor.listener)
	go executor.watchOOM()
}

func (executor *Executor) watchOOM() {
	stopped := false
	scanner := executor.scanner.WaitForNextScan()
	// a set of uuids
	uuids := make(map[uuid.UUID]struct{})
	for !stopped {
		select {
		case oomKilled := <-executor.oomKilled:
			uuids[oomKilled] = struct{}{}
		case <-scanner:
			scanner = executor.scanner.WaitForNextScan()
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
		err = fmt.Errorf("service not found")
	}
	return
}

func (executor *Executor) getContainerDetails(containerID uuid.UUID) (name string, err error) {
	name, ok := executor.scanner.FindContainerNameByID(executor.scanner.GetApplications(), containerID)
	if !ok {
		err = fmt.Errorf("container not found")
	}
	return
}
