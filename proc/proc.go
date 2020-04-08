package proc

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/MagalixCorp/magalix-agent/watcher"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/health-go"
	"github.com/reconquest/karma-go"
	"github.com/reconquest/stats-go"
)

// Database eventer
type Database interface {
	WriteEvent(event *watcher.Event) error
	WriteEvents(events []*watcher.Event) error
}

// Proc events processor
type Proc struct {
	pipes struct {
		pods     chan Pod
		replicas chan ReplicaSpec
	}

	states         *States
	changer        StatusChanger
	database       Database
	threadpool     chan func()
	threadpoolSize int
	health         *health.Health

	// workers are only for testing purposes
	workers *sync.WaitGroup

	synced bool
	sync   *sync.RWMutex
}

// StatusChanger interface for status changer
type StatusChanger interface {
	GetApplicationDesiredServices(uuid.UUID) ([]uuid.UUID, error)
	ChangeStatus(string, uuid.UUID, watcher.Status, *watcher.ContainerStatusSource)
}

// NewProc creates a new processor
func NewProc(
	pods chan Pod, replicas chan ReplicaSpec,
	changer StatusChanger, database Database,
	threads int,
	health *health.Health,
) *Proc {
	proc := &Proc{}
	proc.pipes.pods = pods
	proc.pipes.replicas = replicas
	proc.states = NewStates()
	proc.changer = changer
	proc.database = database
	proc.threadpool = make(chan func(), threads)
	proc.threadpoolSize = threads
	proc.sync = &sync.RWMutex{}
	proc.health = health

	proc.workers = &sync.WaitGroup{}

	return proc
}

// Start starts the processor
func (proc *Proc) Start() {
	go proc.runThreads()

	go func() {
		for {
			proc.process()
		}
	}()
}

func (proc *Proc) process() {
	select {
	case pod := <-proc.pipes.pods:
		proc.threadpool <- func() {
			proc.handlePod(pod)
		}
	case spec := <-proc.pipes.replicas:
		proc.threadpool <- func() {
			proc.handleReplicaSpec(spec)
		}
	}
}

func (proc *Proc) runThreads() {
	for id := 1; id <= proc.threadpoolSize; id++ {
		proc.workers.Add(1)

		go func(id int) {
			defer proc.workers.Done()

			for {
				thread := <-proc.threadpool
				if thread == nil {
					return
				}
				thread()
			}
		}(id)

		debugf(nil, "[%d/%d] thread started", id, proc.threadpoolSize)
	}
}

func (proc *Proc) handlePod(pod Pod) {
	proc.sync.RLock()
	defer proc.sync.RUnlock()

	timestamp := time.Now()

	context := getContext(pod.GetIdentity())

	app, service, err := proc.getState(
		pod.AccountID,
		pod.ApplicationID,
		pod.ServiceID,
	)
	if err != nil {
		errorf(context.Reason(err), "unable to get app and service state for pod: %s", pod.Name)
		proc.health.Alert(err, "pod", "get", "state")
		return
	}
	proc.health.Resolve("pod", "get", "state")

	tracef(
		context.Describe("pod", logger.TraceJSON(pod)),
		"{kubernetes} processing pod",
	)

	for container, state := range pod.Containers {
		updated := false
		WithLock(service, func() {
			updated = !service.IsSameContainerState(container, state)
			if updated {
				service.SetContainerState(container, state)
			}
		})

		if updated {
			subcontext := context

			status, source := GetContainerStateStatus(state)
			if source != nil {
				if source.Reason != "" {
					subcontext = subcontext.Describe("reason", source.Reason)
				}

				if source.ExitCode != nil {
					subcontext = subcontext.Describe(
						"exit_code", *source.ExitCode,
					)
				}

				if source.Signal != nil {
					subcontext = subcontext.Describe(
						"signal", *source.Signal,
					)
				}
			}

			debugf(
				subcontext,
				"container: %s status: %s",
				container, status,
			)

			if !proc.isSynced() {
				proc.updateContainerStatus(
					pod.GetIdentity(),
					container,
					status,
					source,
				)
			}

			proc.writeEvent(
				watcher.NewEventWithSource(
					timestamp, pod.GetIdentity(),
					"container", container.String(),
					"status", status,
					watcher.DefaultEventsOrigin, source, nil,
				),
			)
		}
	}

	pod.Status = GetPodStatus(pod)

	proc.writeEvent(
		watcher.NewEvent(
			timestamp, pod.GetIdentity(),
			"pod", pod.ID,
			"status", pod.Status,
			watcher.DefaultEventsOrigin,
		),
	)

	WithLock(service, func() {
		tracef(context, "setting service pod status %s", pod.Status)

		if pod.Status == watcher.StatusTerminated {
			service.RemovePodStatus(pod.ID)
		} else {
			service.SetPodStatus(pod.ID, pod.Status)
		}
	})

	if !proc.isSynced() {
		tracef(context, "proc is synced, updating service&app status")

		WithLock(service, func() {
			proc.updateServiceStatus(pod.GetIdentity(), service)
		})
		WithLock(app, func() {
			proc.updateAppStatus(pod.GetIdentity(), app)
		})
	} else {
		tracef(
			context,
			"proc is not synced, skipping updating app&service status",
		)
	}
}

func (proc *Proc) handleReplicaSpec(spec ReplicaSpec) {
	proc.sync.RLock()
	defer proc.sync.RUnlock()

	timestamp := time.Now()

	context := getContext(spec.GetIdentity())
	debugf(context, "setting service pods replicas to: %v", spec.Replicas)

	app, service, err := proc.getState(
		spec.AccountID,
		spec.ApplicationID,
		spec.ServiceID,
	)
	if err != nil {
		errorf(context.Reason(err), "unable to get app and service state for replicaspec: %s", spec.Name)
		proc.health.Alert(err, "replica", "get", "state")
		return
	}
	proc.health.Resolve("replica", "get", "state")

	// TODO: add validation for spec.Replicas > 0
	// does kubernetes guaruantees that?

	proc.writeEvent(
		watcher.NewEvent(
			timestamp, spec.GetIdentity(),
			"replicas", spec.ID,
			"replicas", spec.Replicas,
			watcher.DefaultEventsOrigin,
		),
	)

	WithLock(service, func() {
		service.SetReplicas(spec.Replicas)
	})

	if !proc.isSynced() {
		WithLock(service, func() {
			proc.updateServiceStatus(spec.GetIdentity(), service)
		})
		WithLock(app, func() {
			proc.updateAppStatus(spec.GetIdentity(), app)
		})
	}
}

func (proc *Proc) updateServiceStatus(id watcher.Identity, service *ServiceState) bool {
	timestamp := time.Now()

	pods := []watcher.Status{}
	for _, status := range service.pods {
		pods = append(pods, status)
	}

	status := GetServiceStateStatus(id, pods, service.GetReplicas())

	if service.GetStatus() != status {
		service.SetStatus(status)

		proc.changer.ChangeStatus(
			"service", id.ServiceID, status, nil,
		)

		proc.health.Resolve(
			"status", "update", "service",
		)

		stats.Increase("events/service/success")

		go proc.writeEvent(
			watcher.NewEvent(
				timestamp, id,
				"service", id.ServiceID.String(),
				"status", status,
				watcher.DefaultEventsOrigin,
			),
		)

		return true
	}

	return true
}

func (proc *Proc) updateAppStatus(id watcher.Identity, app *AppState) bool {
	timestamp := time.Now()

	services := []*ServiceState{}
	for _, service := range app.services {
		services = append(services, service)
	}

	status := GetAppStateStatus(id, services, len(app.desiredServices))

	if app.GetStatus() != status {
		app.SetStatus(status)

		proc.changer.ChangeStatus(
			"application", id.ApplicationID, status, nil,
		)

		proc.health.Resolve(
			"status", "update", "application",
		)

		stats.Increase("events/application/success")

		go proc.writeEvent(
			watcher.NewEvent(
				timestamp, id,
				"application", id.ApplicationID.String(),
				"status", status,
				watcher.DefaultEventsOrigin,
			),
		)

		return true
	}

	return false
}

func (proc *Proc) updateContainerStatus(
	id watcher.Identity,
	container uuid.UUID,
	status watcher.Status,
	source *watcher.ContainerStatusSource,
) bool {
	proc.changer.ChangeStatus("container", container, status, source)

	return true
}

func (proc *Proc) updateAllStatuses() {
	infof(nil, "updating statuses for all entities after full sync")

	updated := 0
	WithLock(proc.states, func() {
		for appID, app := range proc.states.apps {
			tracef(
				nil,
				"updating statuses for services in application: %s",
				appID,
			)

			WithLock(app, func() {
				for serviceID, service := range app.services {
					tracef(
						karma.Describe("application_id", appID.String()),
						"updating statuses for service: %s",
						serviceID,
					)

					WithLock(service, func() {
						for container, state := range service.containers {
							tracef(
								karma.
									Describe("application_id", appID.String()).
									Describe("service_id", serviceID.String()),
								"updating statuses for container: %s",
								container,
							)

							status, source := GetContainerStateStatus(state)

							if proc.updateContainerStatus(
								watcher.Identity{
									AccountID:     app.accountID,
									ApplicationID: appID,
									ServiceID:     serviceID,
								},
								container,
								status,
								source,
							) {
								updated++
							}
						}

						if proc.updateServiceStatus(
							watcher.Identity{
								AccountID:     app.accountID,
								ApplicationID: appID,
								ServiceID:     serviceID,
							},
							service,
						) {
							updated++
						}
					})
				}

				if proc.updateAppStatus(
					watcher.Identity{
						AccountID:     app.accountID,
						ApplicationID: appID,
					},
					app,
				) {
					updated++
				}
			})
		}
	})

	infof(nil, "after full sync updated %d statuses", updated)
	infof(nil, "statues for all entities updated after full sync")
}

func (proc *Proc) writeEvent(event watcher.Event) bool {
	if proc.database == nil {
		return false
	}
	err := proc.database.WriteEvent(&event)
	if err != nil {
		errorf(err, "unable to write event to database")

		proc.health.Alert(
			karma.Format(err, "problems with writing events to database"),
			"write", "event",
		)

		return false
	}
	proc.health.Resolve("write", "event")

	return true
}

func (proc *Proc) getState(
	accountID uuid.UUID,
	appID uuid.UUID,
	serviceID uuid.UUID,
) (*AppState, *ServiceState, error) {
	var app *AppState
	var service *ServiceState
	var ok bool

	WithLock(proc.states, func() {
		app, ok = proc.states.GetApp(appID)
		if !ok {
			app = proc.states.NewApp(appID, accountID)
		}
	})

	var err error
	WithLock(app, func() {
		service, ok = app.GetService(serviceID)
		if !ok {
			service = app.NewService(serviceID)

			services, apiError := proc.getApplicationDesiredServices(
				appID, serviceID,
			)
			if apiError != nil {
				err = karma.Format(
					apiError,
					"unable to get desired services for application: %s",
					appID,
				)
				err = apiError
				return
			}

			app.SetDesiredServices(services)
		}
	})

	if err != nil {
		return nil, nil, err
	}

	return app, service, nil
}

func (proc *Proc) getApplicationDesiredServices(
	appID uuid.UUID, expectedServiceID uuid.UUID,
) ([]uuid.UUID, error) {
	const (
		retryInterval = time.Second * 3
	)

	var services []uuid.UUID
	var err error
	var retry int

	for {
		retry++

		context := karma.Describe("application_id", appID).
			Describe("expected service_id", expectedServiceID).
			Describe("retry", fmt.Sprint(retry))

		debugf(
			context,
			"requesting list of services for application",
		)

		services, err = proc.changer.GetApplicationDesiredServices(appID)
		if err != nil {
			if karma.Contains(err, watcher.ErrorNoSuchEntity) {
				return nil, err
			}

			errorf(err, "unable to retrieve list of services for application")

			time.Sleep(retryInterval)

			continue
		}

		for _, serviceID := range services {
			if serviceID == expectedServiceID {
				return services, nil
			}
		}

		time.Sleep(retryInterval)
		if retry > 1 {
			return nil, errors.New("unable to get application desired services")
		}
		continue
	}

	return services, nil
}

func (proc *Proc) isSynced() bool {
	return proc.synced
}

// SetSynced sync all entities
func (proc *Proc) SetSynced() {
	proc.updateAllStatuses()
}

func getContext(identity watcher.Identity) *karma.Context {
	context := karma.
		Describe("application_id", identity.ApplicationID).
		Describe("service_id", identity.ServiceID)

	return context
}
