package watcher

import (
	"time"

	"github.com/MagalixTechnologies/uuid-go"
	kapi "k8s.io/api/core/v1"
)

const (
	// DefaultEventsOrigin default origin when not specified
	DefaultEventsOrigin = "watcher"
)

// ContainerState a struct to hold the container state
// it keeps the last termination state to detect OOMs in case
// of CrashLoops
type ContainerState struct {
	Current              kapi.ContainerState
	LastTerminationState kapi.ContainerState
}

// Event structure
type Event struct {
	ID            interface{} `json:"id,omitempty" bson:"id,omitempty"`
	Timestamp     time.Time   `json:"timestamp,omitempty"`
	Entity        string      `json:"entity" bson:"entity"`
	EntityID      string      `json:"entity_id,omitempty" bson:"entity_id,omitempty"`
	AccountID     uuid.UUID   `json:"account_id" bson:"account_id"`
	ClusterID     uuid.UUID   `json:"cluster_id" bson:"cluster_id"`
	NodeID        *uuid.UUID  `json:"node_id" bson:"node_id"`
	ApplicationID *uuid.UUID  `json:"application_id,omitempty" bson:"application_id,omitempty"`
	ServiceID     *uuid.UUID  `json:"service_id,omitempty" bson:"service_id,omitempty"`
	ContainerID   *uuid.UUID  `json:"container_id,omitempty" bson:"container_id,omitempty"`
	Kind          string      `json:"kind" bson:"kind"`
	Value         interface{} `json:"value,omitempty" bson:"value,omitempty"`
	Origin        string      `json:"origin,omitempty" bson:"origin,omitempty"`
	Source        interface{} `json:"source,omitempty" bson:"source,omitempty"`
	Meta          interface{} `json:"meta,omitempty" bson:"meta,omitempty"`
}

// NewEvent creates a new event should be deprecated in favor of NewEventWithSource
func NewEvent(
	timestamp time.Time,
	identity Identity,
	entity string,
	entityID string,
	kind string,
	value interface{},
	origin string,
) Event {
	return NewEventWithSource(
		timestamp, identity, entity, entityID, kind, value, origin, nil, nil,
	)
}

// NewEventWithSource creates a new event
func NewEventWithSource(
	timestamp time.Time,
	identity Identity,
	entity string,
	entityID string,
	kind string,
	value interface{},
	origin string,
	source *ContainerStatusSource,
	meta *string,
) Event {
	var metaInterface interface{}
	if meta != nil {
		metaInterface = meta
	}
	event := Event{
		ID:            uuid.NewV4(),
		Timestamp:     timestamp,
		Entity:        entity,
		EntityID:      entityID,
		AccountID:     identity.AccountID,
		ApplicationID: &identity.ApplicationID,
		ServiceID:     &identity.ServiceID,
		Kind:          kind,
		Value:         value,
		Meta:          metaInterface,
	}

	// json omitempty will not work if source is typed nil
	if source != nil {
		event.Source = source
	}

	return event
}

// Identity a struct to identify an entity
type Identity struct {
	AccountID     uuid.UUID
	ApplicationID uuid.UUID
	ServiceID     uuid.UUID
}
