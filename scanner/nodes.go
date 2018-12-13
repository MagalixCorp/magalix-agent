package scanner

import (
	"github.com/MagalixTechnologies/agent/kuber"
	"github.com/MagalixTechnologies/agent/proto"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
)

func identifyNodes(nodes []kuber.Node, clusterID uuid.UUID) error {
	var err error
	for i, node := range nodes {
		node.ID, err = IdentifyEntity(node.IP, clusterID)
		if err != nil {
			return karma.Format(
				err,
				"unable to generate unique identifier for node: %s", node.IP,
			)
		}
		nodes[i] = node
	}

	return nil
}

func PacketNodes(nodes []kuber.Node) proto.PacketNodesStoreRequest {
	packet := proto.PacketNodesStoreRequest{}
	for _, node := range nodes {

		packet = append(
			packet,
			proto.PacketRegisterNodeItem{
				ID:           node.ID,
				Name:         node.Name,
				IP:           node.IP,
				Region:       node.Region,
				InstanceType: node.InstanceType,
				InstanceSize: node.InstanceSize,
				Containers:   node.Containers,
				Capacity: proto.PacketRegisterNodeCapacityItem(
					node.Capacity,
				),
				Allocatable: proto.PacketRegisterNodeCapacityItem(
					node.Allocatable,
				),
				ContainerList: packetContainerList(node.ContainerList),
			},
		)
	}
	return packet
}

func packetContainerList(containerList []*kuber.Container) []*proto.PacketRegisterNodeContainerListItem {
	if containerList == nil {
		return nil
	}
	var res []*proto.PacketRegisterNodeContainerListItem
	for _, container := range containerList {
		res = append(res,
			&proto.PacketRegisterNodeContainerListItem{
				Cluster:   container.Cluster,
				Image:     container.Image,
				Limits:    packetContainerResources(container.Limits),
				Requests:  packetContainerResources(container.Requests),
				Name:      container.Name,
				Namespace: container.Namespace,
				Node:      container.Node,
				Pod:       container.Pod,
			},
		)
	}
	return res
}

func packetContainerResources(resources *kuber.ContainerResources) *proto.PacketRegisterNodeContainerListResourcesItem {
	if resources == nil {
		return nil
	}
	return &proto.PacketRegisterNodeContainerListResourcesItem{
		CPU:    resources.CPU,
		Memory: resources.Memory,
	}
}

func getNodesTable(nodes []kuber.Node) map[string]uuid.UUID {
	table := map[string]uuid.UUID{}
	for _, node := range nodes {
		table[node.Name] = node.ID
	}
	return table
}
