package proc

import (
	"sync"

	"github.com/MagalixCorp/magalix-agent/v2/watcher"
	"github.com/MagalixTechnologies/core/logger"
	uuid "github.com/MagalixTechnologies/uuid-go"
)

// AppState holds application (namespace) state
type AppState struct {
	accountID       uuid.UUID
	status          watcher.Status
	desiredServices []uuid.UUID
	services        map[uuid.UUID]*ServiceState
	*sync.RWMutex
}

// SetDesiredServices setter for AppState.desiredServices
func (app *AppState) SetDesiredServices(services []uuid.UUID) {
	app.desiredServices = services
}

// GetStatus getter for AppState.status
func (app *AppState) GetStatus() watcher.Status {
	return app.status
}

// SetStatus setter for AppState.status
func (app *AppState) SetStatus(status watcher.Status) {
	app.status = status
}

// GetService gets a service with id
func (app *AppState) GetService(id uuid.UUID) (*ServiceState, bool) {
	service, ok := app.services[id]
	return service, ok
}

// NewService creates a new service with a given id
func (app *AppState) NewService(
	id uuid.UUID,
) *ServiceState {
	service := &ServiceState{
		RWMutex:    &sync.RWMutex{},
		containers: map[uuid.UUID]ContainerState{},
	}

	app.services[id] = service

	return service
}

// GetAppStateStatus a helper function to get the status of the app
func GetAppStateStatus(id watcher.Identity, services []*ServiceState, desired int) watcher.Status {
	var running int
	var errors int
	var pending int
	var completed int

	for _, service := range services {
		switch service.GetStatus() {
		case watcher.StatusRunning:
			running++
		case watcher.StatusPending:
			pending++
		case watcher.StatusCompleted:
			completed++
		case watcher.StatusError:
			errors++
		}
	}

	var status watcher.Status
	switch {
	case errors > 0:
		status = watcher.StatusError

	case desired > 0 && running >= desired:
		status = watcher.StatusRunning

	case desired > 0 && running > 0 && completed > 0 && completed+running >= desired:
		status = watcher.StatusRunning

	case desired > 0 && completed >= desired:
		status = watcher.StatusCompleted

	case desired == 0:
		status = watcher.StatusCompleted

	default:
		status = watcher.StatusPending
	}

	logger.Debugw(
		"application status: "+status.String(),
		"application_id", id.ApplicationID,
		"services/running", running,
		"services/pending", pending,
		"services/completed", completed,
		"services/errors", errors,
		"services/desired", desired,
	)

	return status
}
