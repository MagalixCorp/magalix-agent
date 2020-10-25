package proc

import (
	"sync"

	"github.com/MagalixCorp/magalix-agent/v2/watcher"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/MagalixTechnologies/uuid-go"
	kapi "k8s.io/api/core/v1"
)

// PodName pod name
type PodName = string

// ContainerState a struct to hold the container state
// it keeps the last termination state to detect OOMs in case
// of CrashLoops
type ContainerState struct {
	Current              kapi.ContainerState
	LastTerminationState kapi.ContainerState
}

// ServiceState defines the service state
type ServiceState struct {
	status     watcher.Status
	replicas   int
	pods       map[PodName]watcher.Status
	containers map[uuid.UUID]ContainerState
	*sync.RWMutex
}

// GetStatus getter for service.status
func (service *ServiceState) GetStatus() watcher.Status {
	return service.status
}

// SetStatus setter for service.status
func (service *ServiceState) SetStatus(status watcher.Status) {
	service.status = status
}

// GetPodStatus returns the status of a pod by its name that belongs to the service
func (service *ServiceState) GetPodStatus(name string) (watcher.Status, bool) {
	if service.pods == nil {
		return watcher.StatusUnknown, false
	}

	status, ok := service.pods[name]
	return status, ok
}

// SetPodStatus sets a status for a pod by its name
func (service *ServiceState) SetPodStatus(name string, status watcher.Status) {
	if service.pods == nil {
		service.pods = map[PodName]watcher.Status{}
	}

	service.pods[name] = status
}

// RemovePodStatus removes pod status by name
func (service *ServiceState) RemovePodStatus(name string) {
	delete(service.pods, name)
}

// SetReplicas setter for service,replicas
// represents the number of replicas of the service
func (service *ServiceState) SetReplicas(replicas int) {
	service.replicas = replicas
}

// GetReplicas getter for service.replicas
func (service *ServiceState) GetReplicas() int {
	return service.replicas
}

// IsOOMKilled returns if the service is oom killed
func (state *ContainerState) IsOOMKilled() bool {
	return (state.Current.Terminated != nil && state.Current.Terminated.Reason == "OOMKilled") ||
		(state.Current.Running == nil &&
			state.LastTerminationState.Running == nil &&
			state.LastTerminationState.Terminated != nil &&
			state.LastTerminationState.Terminated.Reason == "OOMKilled")
}

// IsSameContainerState checks if the state is the same for a container inside the service
func (service *ServiceState) IsSameContainerState(
	container uuid.UUID,
	state ContainerState,
) bool {
	prev, ok := service.containers[container]
	if !ok {
		return false
	}

	if prev.Current.Running != nil && state.Current.Running != nil {
		return prev.Current.Running.StartedAt == state.Current.Running.StartedAt
	}

	if prev.Current.Waiting != nil && state.Current.Waiting != nil {
		return prev.Current.Waiting.Reason == state.Current.Waiting.Reason &&
			prev.Current.Waiting.Message == state.Current.Waiting.Message
	}

	if prev.Current.Terminated != nil && state.Current.Terminated != nil {
		return prev.Current.Terminated.StartedAt == state.Current.Terminated.StartedAt &&
			prev.Current.Terminated.FinishedAt == state.Current.Terminated.FinishedAt
	}

	return false
}

// SetContainerState setter for container state
func (service *ServiceState) SetContainerState(
	container uuid.UUID, state ContainerState,
) {
	service.containers[container] = state
}

// GetServiceStateStatus a helper function to get the status of the service
func GetServiceStateStatus(id watcher.Identity, pods []watcher.Status, replicas int) watcher.Status {
	var running int
	var errors int
	var pending int
	var completed int

	for _, status := range pods {
		switch status {
		case watcher.StatusRunning:
			running++

		case watcher.StatusPending:
			pending++

		case watcher.StatusCompleted:
			completed++

		case watcher.StatusTerminated:
			// in magalix terms every terminated service is error
			// it should work or exit with exit-code 0 (StatusCompleted)
			fallthrough

		default:
			errors++
		}
	}

	var status watcher.Status
	switch {
	// it should be first in order because there are cases like
	// zero-downtime deployments where new pod is running but old pod is
	// failed while terminating
	case replicas > 0 && running >= replicas:
		status = watcher.StatusRunning

	case replicas > 0 && completed >= replicas:
		status = watcher.StatusCompleted

	case replicas > 0 &&
		completed > 0 && running >= 0 && running+completed >= replicas:
		status = watcher.StatusCompleted

	case replicas == 0:
		status = watcher.StatusCompleted

	case errors > 0:
		status = watcher.StatusError

	default:
		status = watcher.StatusPending
	}

	logger.Debugw(
		"service status: "+status.String(),
		"application_id", id.ApplicationID,
		"service_id", id.ServiceID,
		"pods/running", running,
		"pods/pending", pending,
		"pods/completed", completed,
		"pods/errors", errors,
		"pods/replicas", replicas,
	)

	return status
}
