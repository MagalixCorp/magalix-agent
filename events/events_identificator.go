package events

import (
	"github.com/MagalixCorp/magalix-agent/v2/proc"
	"github.com/MagalixCorp/magalix-agent/v2/scanner"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
)

// GetID returns resource id
func (eventer *Eventer) GetID(resource proc.Identifiable) (string, error) {
	serviceID, err := eventer.GetServiceID(resource)
	if err != nil {
		return "", karma.Format(
			err,
			"unable to get service eventer",
		)
	}

	kind := resource.GroupVersionKind().Kind
	if kind == "Pod" {
		eventer, err := scanner.IdentifyEntity(resource.GetName(), serviceID)
		if err != nil {
			return "", err
		}

		return eventer.String(), nil
	}

	return serviceID.String(), nil
}

// GetAccountID returns resource account id
func (eventer *Eventer) GetAccountID(
	resource proc.Identifiable,
) (uuid.UUID, error) {
	return eventer.client.AccountID, nil
}

// GetApplicationID returns resource application id
func (eventer *Eventer) GetApplicationID(
	resource proc.Identifiable,
) (uuid.UUID, error) {
	// consider using for range eventer.applications
	return scanner.IdentifyEntity(resource.GetNamespace(), eventer.client.ClusterID)
}

// GetServiceID returns resource service id
func (eventer *Eventer) GetServiceID(
	resource proc.Identifiable,
) (uuid.UUID, error) {
	kind := resource.GroupVersionKind().Kind

	if kind == "Pod" {
		_, serviceID, found := eventer.scanner.FindService(
			resource.GetNamespace(),
			resource.GetName(),
		)
		if !found {
			return serviceID, karma.
				Describe("namespace", resource.GetNamespace()).
				Describe("name", resource.GetName()).
				Reason("no such service")
		}
		return serviceID, nil
	}

	appID, err := eventer.GetApplicationID(resource)
	if err != nil {
		return uuid.Nil, err
	}

	return scanner.IdentifyEntity(resource.GetName(), appID)

}

// GetContainerID returns resource container id
func (eventer *Eventer) GetContainerID(
	pod proc.Identifiable,
	containerName string,
) (uuid.UUID, error) {
	_, _, container, found := eventer.scanner.FindContainer(
		pod.GetNamespace(),
		pod.GetName(),
		containerName,
	)
	if !found {
		return uuid.Nil, karma.
			Describe("namespace", pod.GetNamespace()).
			Describe("name", pod.GetName()).
			Describe("container", containerName).
			Reason("no such container")
	}

	return container.ID, nil
}

// IsIgnored detects if it should be ignored
// It looks if the resource belongs to an ignored namespace
func (eventer *Eventer) IsIgnored(
	resource proc.Identifiable,
) bool {
	return utils.InSkipNamespace(eventer.skipNamespaces, resource.GetNamespace())
}
