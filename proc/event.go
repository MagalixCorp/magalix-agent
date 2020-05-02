package proc

import (
	"github.com/MagalixCorp/magalix-agent/v2/watcher"
	"github.com/MagalixTechnologies/uuid-go"
)

// Pod pod type
type Pod struct {
	Name          string                       `json:"name"`
	ID            string                       `json:"id"`
	AccountID     uuid.UUID                    `json:"account_id"`
	ApplicationID uuid.UUID                    `json:"application_id"`
	ServiceID     uuid.UUID                    `json:"service_id"`
	Status        watcher.Status               `json:"status"`
	Containers    map[uuid.UUID]ContainerState `json:"containers"`
}

// GetIdentity returns an identity for the pod
func (pod *Pod) GetIdentity() watcher.Identity {
	return watcher.Identity{
		AccountID:     pod.AccountID,
		ApplicationID: pod.ApplicationID,
		ServiceID:     pod.ServiceID,
	}
}

// ReplicaSpec returns an identity for a replicated service
type ReplicaSpec struct {
	Name          string
	ID            string
	AccountID     uuid.UUID
	ApplicationID uuid.UUID
	ServiceID     uuid.UUID
	Replicas      int
}

// GetIdentity returns an identity for the service
func (spec *ReplicaSpec) GetIdentity() watcher.Identity {
	return watcher.Identity{
		AccountID:     spec.AccountID,
		ApplicationID: spec.ApplicationID,
		ServiceID:     spec.ServiceID,
	}
}
