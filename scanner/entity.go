package scanner

import (
	"crypto/sha256"
	"regexp"

	"github.com/MagalixCorp/magalix-agent/proto"
	"github.com/MagalixTechnologies/uuid-go"
	kv1 "k8s.io/api/core/v1"
)

// Entity basic entity structure can be an application, a service or a container
type Entity struct {
	ID   uuid.UUID
	Name string
	Kind string

	Annotations map[string]string
}

// IdentifyEntity sets the id of an entity
func (entity *Entity) Identify(parent uuid.UUID) error {
	var err error
	entity.ID, err = IdentifyEntity(entity.Name, parent)
	if err != nil {
		return err
	}

	return nil
}

// Application an abstraction layer representing a namespace
type Application struct {
	Entity

	Services    []*Service
	LimitRanges []kv1.LimitRange
}

// Service an abstraction layer representing a service
// it can be a deployment, replicaset, statefulset, daemonset,
// job, cronjob or an orphan pod
type Service struct {
	Entity

	PodRegexp      *regexp.Regexp
	ReplicasStatus proto.ReplicasStatus

	Containers []*Container
}

// Container represents a single container controlled by a service
// if the container belongs to a pod with no controller, an orphand pod
// service automatically gets created as a parent
type Container struct {
	Entity

	Image     string
	Resources *proto.ContainerResourceRequirements `json:"resources"`
}

func IdentifyEntity(target string, parent uuid.UUID) (uuid.UUID, error) {
	hash := sha256.Sum256(append(parent.Bytes(), target...))
	return uuid.FromBytes(hash[:uuid.Size])
}
