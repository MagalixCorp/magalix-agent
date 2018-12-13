package proc

import "github.com/MagalixTechnologies/uuid-go"
import "k8s.io/apimachinery/pkg/runtime/schema"

// Identificator an interface to represent entities able to identify identifiables
type Identificator interface {
	GetID(resource Identifiable) (string, error)
	GetAccountID(resource Identifiable) (uuid.UUID, error)
	GetApplicationID(resource Identifiable) (uuid.UUID, error)
	GetServiceID(resource Identifiable) (uuid.UUID, error)
	GetContainerID(pod Identifiable, containerName string) (uuid.UUID, error)
	IsIgnored(resource Identifiable) bool
}

// Identifiable an identifiable kubernetes entity
type Identifiable interface {
	GetLabels() map[string]string
	GetNamespace() string
	GetName() string
	GroupVersionKind() schema.GroupVersionKind
}
