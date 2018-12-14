package scanner

import (
	"github.com/MagalixTechnologies/uuid-go"
)

// HistoryPod pod history
type HistoryPod struct {
	serviceID  uuid.UUID
	containers map[string]*Container
}

// HistoryNamespace namespace history
type HistoryNamespace struct {
	applicationID uuid.UUID
	pods          map[string]*HistoryPod
}

// History cluster history
type History struct {
	namespaces map[string]*HistoryNamespace
}

// NewHistory creates new cluster history
func NewHistory() History {
	return History{
		namespaces: map[string]*HistoryNamespace{},
	}
}

// PopulateContainer populate history by a container
func (history *History) PopulateContainer(
	namespace string,
	podName string,
	containerName string,
	applicationID uuid.UUID,
	serviceID uuid.UUID,
	container *Container,
) {
	ns, ok := history.namespaces[namespace]
	if !ok {
		history.namespaces[namespace] = &HistoryNamespace{
			applicationID: applicationID,
			pods:          map[string]*HistoryPod{},
		}

		ns = history.namespaces[namespace]
	}

	pod, ok := ns.pods[podName]
	if !ok {
		ns.pods[podName] = &HistoryPod{
			serviceID:  serviceID,
			containers: map[string]*Container{},
		}

		pod = ns.pods[podName]
	}

	_, ok = pod.containers[containerName]
	if !ok {
		pod.containers[containerName] = container
	}
}

// PopulateService populate history by a service
func (history *History) PopulateService(
	namespace string,
	podName string,
	applicationID uuid.UUID,
	serviceID uuid.UUID,
) {
	ns, ok := history.namespaces[namespace]
	if !ok {
		history.namespaces[namespace] = &HistoryNamespace{
			applicationID: applicationID,
			pods:          map[string]*HistoryPod{},
		}

		ns = history.namespaces[namespace]
	}

	_, ok = ns.pods[podName]
	if !ok {
		ns.pods[podName] = &HistoryPod{
			serviceID:  serviceID,
			containers: map[string]*Container{},
		}
	}
}

// FindContainer find container by names
func (history *History) FindContainer(
	namespace string,
	podName string,
	containerName string,
) (applicationID, serviceID uuid.UUID, container *Container, found bool) {
	if itemNamespace, ok := history.namespaces[namespace]; ok {
		applicationID = itemNamespace.applicationID
		if itemPod, ok := itemNamespace.pods[podName]; ok {
			serviceID = itemPod.serviceID
			if searchContainer, ok := itemPod.containers[containerName]; ok {
				found = true
				container = searchContainer
				return
			}
		}
	}
	return
}

// FindService find service by names
func (history *History) FindService(
	namespace string,
	podName string,
) (applicationID, serviceID uuid.UUID, found bool) {
	if itemNamespace, ok := history.namespaces[namespace]; ok {
		applicationID = itemNamespace.applicationID
		if itemPod, ok := itemNamespace.pods[podName]; ok {
			serviceID = itemPod.serviceID
			found = true
			return
		}
	}
	return
}
