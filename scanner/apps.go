package scanner

import (
	"encoding/json"

	"github.com/MagalixCorp/magalix-agent/proto"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
)

func identifyApplications(
	applications []*Application,
	clusterID uuid.UUID,
) error {
	for _, application := range applications {
		err := application.Identify(clusterID)
		if err != nil {
			return karma.Format(
				err,
				"unable to assign UUID to application %q",
				application.Name,
			)
		}

		for _, service := range application.Services {
			err := service.Identify(application.ID)
			if err != nil {
				return karma.Format(
					err,
					"unable to assign UUID to service %q",
					service.Name,
				)
			}

			for _, container := range service.Containers {
				err := container.Identify(service.ID)
				if err != nil {
					return karma.Format(
						err,
						"unable to assign UUID to container %q",
						container.Name,
					)
				}
			}
		}
	}

	return nil
}

func PacketApplications(applications []*Application) proto.PacketApplicationsStoreRequest {
	packet := proto.PacketApplicationsStoreRequest{}

	for _, application := range applications {
		services := []proto.PacketRegisterServiceItem{}

		for _, service := range application.Services {
			containers := []proto.PacketRegisterContainerItem{}

			for _, container := range service.Containers {
				// TODO: converting here to json for gob encoding, properly fix later
				resources, _ := json.Marshal(container.Resources)
				lProbe, _ := json.Marshal(container.LivenessProbe)
				rProbe, _ := json.Marshal(container.ReadinessProbe)

				containers = append(
					containers,
					proto.PacketRegisterContainerItem{
						PacketRegisterEntityItem: proto.PacketRegisterEntityItem(container.Entity),

						Image:     container.Image,
						Resources: resources,

						LivenessProbe:  lProbe,
						ReadinessProbe: rProbe,
					},
				)
			}

			services = append(services, proto.PacketRegisterServiceItem{
				PacketRegisterEntityItem: proto.PacketRegisterEntityItem(service.Entity),
				ReplicasStatus:           service.ReplicasStatus,
				Containers:               containers,
			})
		}

		packet = append(
			packet,
			proto.PacketRegisterApplicationItem{
				PacketRegisterEntityItem: proto.PacketRegisterEntityItem(application.Entity),
				Services:                 services,
				LimitRanges:              application.LimitRanges,
			},
		)
	}

	return packet
}
