package metrics

import (
	"github.com/MagalixCorp/magalix-agent/scanner"
	"github.com/prometheus/client_model/go"
)

const (
	NodesCountName = "stats_nodes_count"
	NodesCountHelp = "Current count of nodes."

	InstanceGroupsName = "stats_nodes_instance_groups"
	InstanceGroupsHelp = "Current count of nodes in an instance group."
	InstanceGroupTag   = "instance_group"

	NodeCapacityCpuName = "kube_node_status_capacity_cpu_cores"
	NodeCapacityCpuHelp = "The total CPU resources of the node."

	NodeCapacityMemoryName = "kube_node_status_capacity_memory_bytes"
	NodeCapacityMemoryHelp = "The total memory resources of the node."

	NodeCapacityPodsName = "kube_node_status_capacity_pods"
	NodeCapacityPodsHelp = "The total pod resources of the node."

	NodeAllocatableCpuName = "kube_node_status_allocatable_cpu_cores"
	NodeAllocatableCpuHelp = "The CPU resources of a node that are available for scheduling."

	NodeAllocatableMemoryName = "kube_node_status_allocatable_memory_bytes"
	NodeAllocatableMemoryHelp = "The memory resources of a node that are available for scheduling."

	NodeAllocatablePodsName = "kube_node_status_allocatable_pods"
	NodeAllocatablePodsHelp = "The pod resources of a node that are available for scheduling."

	ContainerRequestsCpuName = "kube_pod_container_resource_requests_cpu_cores"
	ContainerRequestsCpuHelp = "The number of requested cpu cores by a container."

	ContainerLimitsCpuName = "kube_pod_container_resource_limits_cpu_cores"
	ContainerLimitsCpuHelp = "The limit on cpu cores to be used by a container."

	ContainerRequestsMemoryName = "kube_pod_container_resource_requests_memory_bytes"
	ContainerRequestsMemoryHelp = "The number of requested memory bytes by a container."

	ContainerLimitsMemoryName = "kube_pod_container_resource_limits_memory_bytes"
	ContainerLimitsMemoryHelp = "The limit on memory to be used by a container in bytes."

	NodeTag      = "node"
	NamespaceTag = "namespace"
	PodTag       = "pod_name"
	ContainerTag = "container_name"
)

var (
	TypeGAUGE = io_prometheus_client.MetricType_GAUGE.String()
)

type Stats struct {
	scanner *scanner.Scanner
}

func NewStats(s *scanner.Scanner) *Stats {
	return &Stats{
		scanner: s,
	}
}

func (stats *Stats) GetMetrics() (*MetricsBatch, error) {
	scanner2 := stats.scanner

	// wait for next scan
	<-scanner2.WaitForNextTick()

	nodes := scanner2.GetNodes()

	batch := &MetricsBatch{
		Timestamp: scanner2.NodesLastScanTime(),
		Metrics:   map[string]*MetricFamily{},
	}

	batch.Metrics[NodesCountName] = &MetricFamily{
		Name: NodesCountName,
		Help: NodesCountHelp,
		Type: TypeGAUGE,

		Values: []*MetricValue{{Value: float64(len(nodes))}},
	}

	instanceGroups := map[string]int64{}
	for _, node := range nodes {
		instanceGroup := ""
		if node.InstanceType != "" {
			instanceGroup = node.InstanceType
		}
		if node.InstanceSize != "" {
			instanceGroup += "." + node.InstanceSize
		}

		if _, ok := instanceGroups[instanceGroup]; !ok {
			instanceGroups[instanceGroup] = 0
		}

		instanceGroups[instanceGroup] = instanceGroups[instanceGroup] + 1
	}

	instanceGroupsMetric := &MetricFamily{
		Name: InstanceGroupsName,
		Help: InstanceGroupsHelp,
		Type: TypeGAUGE,
		Tags: []string{InstanceGroupTag},

		Values: make([]*MetricValue, len(instanceGroups)),
	}

	i := 0
	for instanceGroup, nodesCount := range instanceGroups {
		mv := &MetricValue{
			Tags:  map[string]string{},
			Value: float64(nodesCount),
		}
		mv.Tags[InstanceGroupTag] = instanceGroup
		instanceGroupsMetric.Values[i] = mv
		i++
	}

	batch.Metrics = appendFamily(
		batch.Metrics,
		instanceGroupsMetric,
	)

	nodesTagsNames := []string{NodeTag}
	for _, node := range nodes {
		nodeEntities := &Entities{
			Node: &node.ID,
		}
		nodeTags := map[string]string{}
		nodeTags[NodeTag] = node.Name

		batch.Metrics = appendFamily(
			batch.Metrics,

			&MetricFamily{
				Name: NodeCapacityCpuName,
				Help: NodeCapacityCpuHelp,
				Type: TypeGAUGE,
				Tags: nodesTagsNames,
				Values: []*MetricValue{
					{
						Entities: nodeEntities,
						Tags:     nodeTags,
						Value:    float64(node.Capacity.CPU),
					},
				},
			},
			&MetricFamily{
				Name: NodeCapacityMemoryName,
				Help: NodeCapacityMemoryHelp,
				Type: TypeGAUGE,
				Tags: nodesTagsNames,
				Values: []*MetricValue{
					{
						Entities: nodeEntities,
						Tags:     nodeTags,
						Value:    float64(node.Capacity.Memory),
					},
				},
			},
			&MetricFamily{
				Name: NodeCapacityPodsName,
				Help: NodeCapacityPodsHelp,
				Type: TypeGAUGE,
				Tags: nodesTagsNames,
				Values: []*MetricValue{
					{
						Entities: nodeEntities,
						Tags:     nodeTags,
						Value:    float64(node.Capacity.Pods),
					},
				},
			},

			&MetricFamily{
				Name: NodeAllocatableCpuName,
				Help: NodeAllocatableCpuHelp,
				Type: TypeGAUGE,
				Tags: nodesTagsNames,
				Values: []*MetricValue{
					{
						Entities: nodeEntities,
						Tags:     nodeTags,
						Value:    float64(node.Allocatable.CPU),
					},
				},
			},
			&MetricFamily{
				Name: NodeAllocatableMemoryName,
				Help: NodeAllocatableMemoryHelp,
				Type: TypeGAUGE,
				Tags: nodesTagsNames,
				Values: []*MetricValue{
					{
						Entities: nodeEntities,
						Tags:     nodeTags,
						Value:    float64(node.Allocatable.Memory),
					},
				},
			},
			&MetricFamily{
				Name: NodeAllocatablePodsName,
				Help: NodeAllocatablePodsHelp,
				Type: TypeGAUGE,
				Tags: nodesTagsNames,
				Values: []*MetricValue{
					{
						Entities: nodeEntities,
						Tags:     nodeTags,
						Value:    float64(node.Allocatable.Pods),
					},
				},
			},

		)

	}

	apps := scanner2.GetApplications()

	containersTagsNames := []string{NamespaceTag, PodTag, ContainerTag}

	containersRequestsCpu := &MetricFamily{
		Name:   ContainerRequestsCpuName,
		Help:   ContainerRequestsCpuHelp,
		Type:   TypeGAUGE,
		Tags:   containersTagsNames,
		Values: []*MetricValue{},
	}
	containersLimitsCpu := &MetricFamily{
		Name:   ContainerLimitsCpuName,
		Help:   ContainerLimitsCpuHelp,
		Type:   TypeGAUGE,
		Tags:   containersTagsNames,
		Values: []*MetricValue{},
	}
	containersRequestsMemory := &MetricFamily{
		Name:   ContainerRequestsMemoryName,
		Help:   ContainerRequestsMemoryHelp,
		Type:   TypeGAUGE,
		Tags:   containersTagsNames,
		Values: []*MetricValue{},
	}
	containersLimitsMemory := &MetricFamily{
		Name:   ContainerLimitsMemoryName,
		Help:   ContainerLimitsMemoryHelp,
		Type:   TypeGAUGE,
		Tags:   containersTagsNames,
		Values: []*MetricValue{},
	}

	for _, app := range apps {
		for _, service := range app.Services {
			for _, container := range service.Containers {
				containerEntities := &Entities{
					Application: &app.ID,
					Service:     &service.ID,
					Container:   &container.ID,
				}
				containerTags := map[string]string{}
				containerTags[NamespaceTag] = app.Name
				containerTags[PodTag] = service.Name
				containerTags[ContainerTag] = container.Name

				containersRequestsCpu.Values = append(
					containersRequestsCpu.Values,
					&MetricValue{
						Entities: containerEntities,
						Tags:     containerTags,
						Value:    float64(container.Resources.Requests.Cpu().MilliValue() / 1000),
					},
				)

				containersLimitsCpu.Values = append(
					containersLimitsCpu.Values,
					&MetricValue{
						Entities: containerEntities,
						Tags:     containerTags,
						Value:    float64(container.Resources.Limits.Cpu().MilliValue() / 1000),
					},
				)

				containersRequestsMemory.Values = append(
					containersRequestsMemory.Values,
					&MetricValue{
						Entities: containerEntities,
						Tags:     containerTags,
						Value:    float64(container.Resources.Requests.Memory().Value()),
					},
				)
				containersLimitsMemory.Values = append(
					containersLimitsMemory.Values,
					&MetricValue{
						Entities: containerEntities,
						Tags:     containerTags,
						Value:    float64(container.Resources.Limits.Memory().Value()),
					},
				)

			}

		}
	}

	batch.Metrics = appendFamily(
		batch.Metrics,
		containersRequestsCpu,
		containersLimitsCpu,
		containersRequestsMemory,
		containersLimitsMemory,
	)

	return batch, nil
}
