package kuber

import (
	"fmt"
	"strings"

	"github.com/MagalixTechnologies/uuid-go"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const NodeRoleLabelPrefix = "node-role.kubernetes.io/"

type Node struct {
	ID            uuid.UUID    `json:"id,omitempty"`
	Name          string       `json:"name"`
	IP            string       `json:"ip"`
	Roles         string       `json:"roles"`
	KubeletPort   int32        `json:"port"`
	Provider      string       `json:"provider,omitempty"`
	Region        string       `json:"region,omitempty"`
	InstanceType  string       `json:"instance_type,omitempty"`
	InstanceSize  string       `json:"instance_size,omitempty"`
	Capacity      NodeCapacity `json:"capacity"`
	Allocatable   NodeCapacity `json:"allocatable"`
	Containers    int          `json:"containers,omitempty"`
	ContainerList []*Container `json:"container_list,omitempty"`
}

// Container user type.
type Container struct {
	// cluster where host of container located in
	Cluster string `json:"cluster,omitempty"`
	// image of container
	Image string `json:"image"`
	// limits of container
	Limits *ContainerResources `json:"limits,omitempty"`
	// requests of container
	Requests *ContainerResources `json:"requests,omitempty"`
	// name of container (not guaranteed to be unique in cluster scope)
	Name string `json:"name"`
	// namespace where pod located in
	Namespace string `json:"namespace"`
	// node where container located in
	Node string `json:"node"`
	// pod where container located in
	Pod string `json:"pod"`
}

// ContainerResources user type.
type ContainerResources struct {
	CPU    int `json:"cpu"`
	Memory int `json:"memory"`
}

func resourceListToContainerResources(resourceList corev1.ResourceList) *ContainerResources {
	res := &ContainerResources{}

	if memory := resourceList.Memory(); memory != nil {
		// TODO: account for differences between megabytes and mebibytes
		memoryMB := memory.ScaledValue(resource.Mega)
		res.Memory = int(memoryMB)
	}

	if cpu := resourceList.Cpu(); cpu != nil && !cpu.IsZero() {
		res.CPU = int(cpu.MilliValue())
	}

	return res
}

type NodeCapacity struct {
	CPU              int `json:"cpu"`
	Memory           int `json:"memory"`
	StorageEphemeral int `json:"storage_ephemeral"`
	Pods             int `json:"pods"`
}

func GetContainersByNode(pods []corev1.Pod) map[string]int {
	containers := map[string]int{}
	for _, pod := range pods {
		containers[pod.Spec.NodeName] += len(pod.Spec.Containers)
	}
	return containers
}

func UpdateNodesContainers(nodes []Node, containers map[string]int) []Node {
	for n, node := range nodes {
		node.Containers, _ = containers[node.Name]
		nodes[n] = node
	}
	return nodes
}

func AddContainerListToNodes(
	nodes []Node,
	pods []corev1.Pod,
	namespace *string,
	pod *string,
	container *string,
) []Node {
	containers, err := GetContainers(pods, namespace, pod, container)
	if err != nil {
		return nil
	}

	for k := range nodes {
		node := &nodes[k]
		node.ContainerList = make([]*Container, 0)
		for _, container := range containers {
			if container.Node == node.Name {
				node.ContainerList = append(node.ContainerList, container)
			}
		}
	}

	return nodes
}

func RangePods(
	pods []corev1.Pod,
	fn func(corev1.Pod) bool) error {

	for _, pod := range pods {
		if !fn(pod) {
			return nil
		}
	}

	return nil
}

func GetContainers(
	pods []corev1.Pod,
	namespace *string,
	pod *string,
	container *string,
) ([]*Container, error) {
	containers := []*Container{}

	err := RangePods(
		pods,
		func(kpod corev1.Pod) bool {
			if namespace != nil && kpod.Namespace != *namespace {
				return true
			}

			if pod != nil && kpod.Name != *pod {
				return true
			}

			for _, kcontainer := range kpod.Spec.Containers {
				if container != nil && kcontainer.Name != *container {
					continue
				}

				limits := kcontainer.Resources.Limits
				requests := kcontainer.Resources.Requests

				containers = append(containers, &Container{
					Node:      kpod.Spec.NodeName,
					Pod:       kpod.Name,
					Namespace: kpod.Namespace,
					Name:      kcontainer.Name,
					Image:     kcontainer.Image,
					Limits:    resourceListToContainerResources(limits),
					Requests:  resourceListToContainerResources(requests),
				})
			}

			return true
		})
	if err != nil {
		return nil, err
	}

	return containers, nil
}

func GetNodes(nodes []corev1.Node) []Node {
	result := []Node{}

	for _, node := range nodes {
		labels := node.Labels

		var address string
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				address = addr.Address
			}
		}

		instanceType, cloudProvider := labels["beta.kubernetes.io/instance-type"]
		instanceSize := ""

		capacity := GetNodeCapacity(node.Status.Capacity)

		if cloudProvider {
			_, gcloud := labels["cloud.google.com/gke-nodepool"]
			if gcloud {
				if strings.Contains(instanceType, "-") {
					parts := strings.SplitN(instanceType, "-", 2)
					instanceType, instanceSize = parts[0], parts[1]
				}
			} else {
				if strings.Contains(instanceType, ".") {
					parts := strings.SplitN(instanceType, ".", 2)
					instanceType, instanceSize = parts[0], parts[1]
				}
			}
		} else {
			// for custom on-perm clusters we use node capacity as instance type
			instanceType = "custom"

			cpuCores := capacity.CPU / 1000
			memoryGi := float64(capacity.Memory) / 1024 / 1024 / 1024

			instanceSize = fmt.Sprintf(
				"cpu-%d--memory-%.2f",
				cpuCores,
				memoryGi,
			)
		}

		provider := strings.Split(node.Spec.ProviderID, ":")[0]

		var nodeRoles []string

		for key := range labels {
			if strings.HasPrefix(key, NodeRoleLabelPrefix) {
				nodeRoles = append(
					nodeRoles,
					strings.Replace(key, NodeRoleLabelPrefix, "", 1),
				)
				break
			}
		}

		result = append(result, Node{
			Name:         node.ObjectMeta.Name,
			IP:           address,
			Roles:        strings.Join(nodeRoles, ","),
			KubeletPort:  node.Status.DaemonEndpoints.KubeletEndpoint.Port,
			Region:       labels["failure-domain.beta.kubernetes.io/region"],
			InstanceType: instanceType,
			InstanceSize: instanceSize,
			Provider:     provider,
			Capacity:     capacity,
			Allocatable:  GetNodeCapacity(node.Status.Allocatable),
		})
	}

	return result
}

func GetNodeCapacity(resources corev1.ResourceList) NodeCapacity {
	capacity := NodeCapacity{
		CPU:              int(resources.Cpu().MilliValue()),
		Memory:           int(resources.Memory().Value()),
		StorageEphemeral: int(resources.StorageEphemeral().Value()),
		Pods:             int(resources.Pods().Value()),
	}

	return capacity
}
