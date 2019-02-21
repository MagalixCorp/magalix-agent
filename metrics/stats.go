package metrics

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixCorp/magalix-agent/scanner"
	"github.com/MagalixTechnologies/log-go"
	"github.com/prometheus/client_model/go"
	"github.com/reconquest/karma-go"
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

	ContainerRequestsCpuName = "stats_container_resource_requests_cpu_cores"
	ContainerRequestsCpuHelp = "The number of requested cpu cores by a magalix container."

	ContainerLimitsCpuName = "stats_container_resource_limits_cpu_cores"
	ContainerLimitsCpuHelp = "The limit on cpu cores to be used by a magalix container."

	ContainerRequestsMemoryName = "stats_container_resource_requests_memory_bytes"
	ContainerRequestsMemoryHelp = "The number of requested memory bytes by a magalix container."

	ContainerLimitsMemoryName = "stats_container_resource_limits_memory_bytes"
	ContainerLimitsMemoryHelp = "The limit on memory to be used by a magalix container in bytes."

	NodeTag      = "nodename"
	NamespaceTag = "namespace"
	PodTag       = "pod_name"
	ContainerTag = "container_name"
)

var (
	TypeGAUGE = io_prometheus_client.MetricType_GAUGE.String()
)

type Stats struct {
	*log.Logger

	scanner *scanner.Scanner
}

func NewStats(s *scanner.Scanner, logger *log.Logger) *Stats {
	return &Stats{
		Logger:  logger,
		scanner: s,
	}
}

func (stats *Stats) GetMetrics(tickTime time.Time) (
	map[string]*MetricFamily,
	error,
) {
	// wait for the same tickTime
	<-stats.scanner.WaitForTick(tickTime)

	ctx := karma.Describe("tick_time", tickTime.Format(time.RFC3339))

	stats.Infof(
		ctx,
		"{stats} requesting metrics from scanner",
	)

	nodes := stats.scanner.GetNodes()
	apps := stats.scanner.GetApplications()

	metrics := map[string]*MetricFamily{}

	metrics = appendFamily(
		metrics,
		nodesCount(nodes),
		instanceGroups(nodes),
	)

	metrics = mergeFamilies(metrics, nodesResources(nodes))
	metrics = mergeFamilies(metrics, containersResources(apps))

	stats.Infof(
		ctx,
		"{stats} collected %v metrics",
		len(metrics),
	)

	return metrics, nil
}

func containersResources(apps []*scanner.Application) map[string]*MetricFamily {
	containerResources := map[string]*MetricFamily{}

	// TODO: track the host node of the container
	// TODO per replica tracking
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

	containerResources = appendFamily(
		containerResources,

		containersRequestsCpu,
		containersLimitsCpu,
		containersRequestsMemory,
		containersLimitsMemory,
	)

	return containerResources
}

func instanceGroups(nodes []kuber.Node) *MetricFamily {
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
			Entities: &Entities{},
			Tags:     map[string]string{},
			Value:    float64(nodesCount),
		}
		mv.Tags[InstanceGroupTag] = instanceGroup
		instanceGroupsMetric.Values[i] = mv
		i++
	}
	return instanceGroupsMetric
}

func nodesCount(nodes []kuber.Node) *MetricFamily {
	return &MetricFamily{
		Name: NodesCountName,
		Help: NodesCountHelp,
		Type: TypeGAUGE,

		Values: []*MetricValue{
			{
				Entities: &Entities{},
				Value:    float64(len(nodes)),
			},
		},
	}
}

func nodesResources(nodes []kuber.Node) map[string]*MetricFamily {
	nodeMetrics := map[string]*MetricFamily{}
	nodesTagsNames := []string{NodeTag}
	for _, node := range nodes {
		nodeEntities := &Entities{
			Node: &node.ID,
		}
		nodeTags := map[string]string{}
		nodeTags[NodeTag] = node.Name

		nodeMetrics = appendFamily(
			nodeMetrics,

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

	return nodeMetrics
}
