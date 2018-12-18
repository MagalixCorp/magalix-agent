package scanner

import (
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/MagalixCorp/magalix-agent/client"
	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixCorp/magalix-agent/proto"
	"github.com/MagalixCorp/magalix-agent/utils"
	"github.com/MagalixTechnologies/log-go"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
	kv1 "k8s.io/api/core/v1"
	kresource "k8s.io/apimachinery/pkg/api/resource"
)

const (
	timeoutScannerBackoff = time.Second * 5
	intervalScanner       = time.Minute * 1
)

// Scanner cluster scanner
type Scanner struct {
	client         *client.Client
	logger         *log.Logger
	kube           *kuber.Kube
	skipNamespaces []string
	accountID      uuid.UUID
	clusterID      uuid.UUID

	apps         []*Application
	appsLastScan time.Time

	pods []kv1.Pod

	nodes         []kuber.Node
	nodesLastScan time.Time

	history History
	mutex   *sync.Mutex

	dones []chan struct{}
}

// InitScanner creates a new scanner then Start it
func InitScanner(
	client *client.Client,
	kube *kuber.Kube,
	skipNamespaces []string,
	accountID uuid.UUID,
	clusterID uuid.UUID,
) *Scanner {
	scanner := NewScanner(client, kube, skipNamespaces, accountID, clusterID)
	scanner.Start()
	return scanner
}

// NewScanner creates a new scanner
func NewScanner(
	client *client.Client,
	kube *kuber.Kube,
	skipNamespaces []string,
	accountID uuid.UUID,
	clusterID uuid.UUID,
) *Scanner {
	scanner := &Scanner{
		client:         client,
		logger:         client.Logger,
		kube:           kube,
		skipNamespaces: skipNamespaces,
		accountID:      accountID,
		clusterID:      clusterID,
		history:        NewHistory(),

		mutex: &sync.Mutex{},
		dones: make([]chan struct{}, 0),
	}

	return scanner
}

func nextTick(interval time.Duration) <-chan time.Time {
	if time.Hour%interval == 0 {
		now := time.Now()
		// TODO: sub seconds
		nanos := time.Second*time.Duration(now.Second()) + time.Minute*time.Duration(now.Minute())
		next := interval - nanos%interval
		return time.After(next)
	}
	return time.After(interval)
}

// Start start scanner
func (scanner *Scanner) Start() {
	scanner.mutex.Lock()
	defer scanner.mutex.Unlock()

	// block for first scan
	scanner.scanNodes()
	scanner.scanApplications()

	go func() {
		ticker := nextTick(intervalScanner)
		for {
			<-ticker
			scanner.scanNodes()
			scanner.scanApplications()

			// unlocks all go routines waiting for the next scan
			scanner.sendDones()
			ticker = nextTick(intervalScanner)
		}
	}()
}

// WaitForNextScan returns a signal channel that gets unblocked after the next scan
// Example usage:
//  <- scanner.WaitForNextScan()
func (scanner *Scanner) WaitForNextScan() chan struct{} {
	scanner.mutex.Lock()
	defer scanner.mutex.Unlock()
	done := make(chan struct{})
	scanner.dones = append(scanner.dones, done)
	return done
}

func (scanner *Scanner) sendDones() {
	scanner.mutex.Lock()
	defer scanner.mutex.Unlock()
	for _, done := range scanner.dones {
		done <- struct{}{}
		close(done)
	}
	scanner.dones = make([]chan struct{}, 0)
}

// GetApplications get scanned applications
func (scanner *Scanner) GetApplications() []*Application {
	scanner.mutex.Lock()
	defer scanner.mutex.Unlock()

	apps := make([]*Application, len(scanner.apps))
	copy(apps, scanner.apps)
	return apps
}

// GetNodes get scanned nodes
func (scanner *Scanner) GetNodes() []kuber.Node {
	scanner.mutex.Lock()
	defer scanner.mutex.Unlock()

	nodes := make([]kuber.Node, len(scanner.nodes))
	copy(nodes, scanner.nodes)

	return nodes
}

// GetNodesAddresses get addresses of scanned nodes
func (scanner *Scanner) GetNodesAddresses() []string {
	nodes := scanner.GetNodes()

	addresses := []string{}
	for _, node := range nodes {
		if node.IP != "" {
			addresses = append(addresses, node.IP)
		}
	}

	//return []string{"127.0.0.1"}
	return addresses
}

func (scanner *Scanner) getNodes() ([]kuber.Node, *kv1.NodeList, error) {
	nodeList, err := scanner.kube.GetNodes()
	if err != nil {
		return nil, nil, err
	}

	pods, err := scanner.kube.GetPods()
	if err != nil {
		return nil, nil, err
	}

	nodes := kuber.UpdateNodesContainers(
		kuber.GetNodes(nodeList.Items),
		kuber.GetContainersByNode(pods),
	)

	nodes = kuber.AddContainerListToNodes(
		nodes,
		pods,
		nil,
		nil,
		nil,
	)

	err = identifyNodes(nodes, scanner.clusterID)
	if err != nil {
		return nil, nil, karma.Format(
			err,
			"unable to obtain unique identifiers for nodes",
		)
	}

	return nodes, nodeList, nil
}

func (scanner *Scanner) scanApplications() {
	for {
		scanner.logger.Infof(nil, "scanning kubernetes applications")

		apps, rawResources, err := scanner.getApplications()
		if err != nil {
			scanner.logger.Errorf(err, "unable to scan kubernetes applications")
			time.Sleep(timeoutScannerBackoff)
			continue
		}

		scanner.logger.Infof(
			nil,
			"found %d kubernetes applications, sending to the gateway",
			len(apps),
		)

		scanner.logger.Tracef(
			karma.Describe("apps", scanner.logger.TraceJSON(apps)),
			"sending applications to the gateway",
		)

		scanner.apps = apps
		scanner.appsLastScan = time.Now()

		scanner.SendApplications(apps)
		scanner.client.SendRaw(rawResources)

		scanner.logger.Infof(
			nil,
			"applications sent",
		)
		break
	}
}

func (scanner *Scanner) scanNodes() {
	for {
		scanner.logger.Infof(nil, "scanning kubernetes nodes")

		nodes, nodeList, err := scanner.getNodes()
		if err != nil {
			scanner.logger.Errorf(err, "unable to scan kubernetes nodes")
			time.Sleep(timeoutScannerBackoff)
			continue
		}

		scanner.logger.Infof(
			nil,
			"found %d kubernetes nodes, sending to the gateway",
			len(nodes),
		)

		scanner.nodes = nodes
		scanner.nodesLastScan = time.Now()

		scanner.SendNodes(nodes)
		scanner.client.SendRaw(map[string]interface{}{
			"nodes": nodeList,
		})

		scanner.logger.Infof(
			nil,
			"nodes sent",
		)
		break
	}
}

// getLimitRangesForNamespace returns all LimitRanges for a specific namespace.
func getLimitRangesForNamespace(
	limitRanges []kv1.LimitRange,
	namespace string,
) []kv1.LimitRange {
	var ranges []kv1.LimitRange

	for index, limit := range limitRanges {
		if limit.GetNamespace() == namespace {
			ranges = append(ranges, limitRanges[index])
		}
	}

	return ranges
}

func (scanner *Scanner) getApplications() ([]*Application, map[string]interface{}, error) {
	controllers, err := scanner.kube.GetReplicationControllers()
	if err != nil {
		scanner.logger.Errorf(
			err,
			"unable to get replication controllers",
		)
	}

	pods, err := scanner.kube.GetPods()
	if err != nil {
		scanner.logger.Errorf(
			err,
			"unable to get pods",
		)
	}
	scanner.pods = pods

	deployments, err := scanner.kube.GetDeployments()
	if err != nil {
		scanner.logger.Errorf(
			err,
			"unable to get deployments",
		)
	}

	statefulSets, err := scanner.kube.GetStatefulSets()
	if err != nil {
		scanner.logger.Errorf(
			err,
			"unable to get statefulSets",
		)
	}

	daemonSets, err := scanner.kube.GetDaemonSets()
	if err != nil {
		scanner.logger.Errorf(
			err,
			"unable to get daemonSets",
		)
	}

	replicaSets, err := scanner.kube.GetReplicaSets()
	if err != nil {
		scanner.logger.Errorf(
			err,
			"unable to get replicasets",
		)
	}

	cronJobs, err := scanner.kube.GetCronJobs()
	if err != nil {
		scanner.logger.Errorf(
			err,
			"unable to get cron jobs",
		)
	}

	limitRanges, err := scanner.kube.GetLimitRanges()
	if err != nil {
		scanner.logger.Errorf(
			err,
			"unable to get limitRanges",
		)
	}

	rawResources := map[string]interface{}{
		"pods":         pods,
		"deployments":  deployments,
		"statefulSets": statefulSets,
		"daemonSets":   daemonSets,
		"replicaSets":  replicaSets,
		"cronJobs":     cronJobs,
		"limitRanges":  limitRanges,
	}

	type Resource struct {
		Namespace      string
		Name           string
		Kind           string
		Annotations    map[string]string
		ReplicasStatus proto.ReplicasStatus
		Containers     []kv1.Container
		PodRegexp      *regexp.Regexp
	}

	resources := []Resource{}

	if pods != nil {
		for _, pod := range pods {
			if len(pod.OwnerReferences) > 0 {
				continue
			}
			resources = append(resources, Resource{
				Kind:       "OrphanPod",
				Namespace:  pod.Namespace,
				Name:       pod.Name,
				Containers: pod.Spec.Containers,
				PodRegexp: regexp.MustCompile(
					fmt.Sprintf(
						"^%s$",
						regexp.QuoteMeta(pod.Name),
					),
				),
				ReplicasStatus: proto.ReplicasStatus{
					Desired:   newInt32Pointer(1),
					Ready:     newInt32Pointer(1),
					Available: newInt32Pointer(1),
				},
			})
		}
	}

	if controllers != nil {
		for _, controller := range controllers.Items {
			resources = append(resources, Resource{
				Kind:        "ReplicationController",
				Annotations: controller.Annotations,
				Namespace:   controller.Namespace,
				Name:        controller.Name,
				Containers:  controller.Spec.Template.Spec.Containers,
				PodRegexp: regexp.MustCompile(
					fmt.Sprintf(
						"^%s-[^-]+$",
						regexp.QuoteMeta(controller.Name),
					),
				),
				ReplicasStatus: proto.ReplicasStatus{
					Desired:   newInt32Pointer(controller.Status.Replicas),
					Ready:     newInt32Pointer(controller.Status.ReadyReplicas),
					Available: newInt32Pointer(controller.Status.AvailableReplicas),
				},
			})
		}
	}

	if deployments != nil {
		for _, deployment := range deployments.Items {
			resources = append(resources, Resource{
				Kind:        "Deployment",
				Annotations: deployment.Annotations,
				Namespace:   deployment.Namespace,
				Name:        deployment.Name,
				Containers:  deployment.Spec.Template.Spec.Containers,
				PodRegexp: regexp.MustCompile(
					fmt.Sprintf(
						"^%s-[^-]+-[^-]+$",
						regexp.QuoteMeta(deployment.Name),
					),
				),
				ReplicasStatus: proto.ReplicasStatus{
					Desired:   newInt32Pointer(deployment.Status.Replicas),
					Ready:     newInt32Pointer(deployment.Status.ReadyReplicas),
					Available: newInt32Pointer(deployment.Status.AvailableReplicas),
				},
			})
		}
	}

	if statefulSets != nil {
		for _, set := range statefulSets.Items {
			resources = append(resources, Resource{
				Kind:        "StatefulSet",
				Annotations: set.Annotations,
				Namespace:   set.Namespace,
				Name:        set.Name,
				Containers:  set.Spec.Template.Spec.Containers,
				PodRegexp: regexp.MustCompile(
					fmt.Sprintf(
						"^%s-([0-9]+)$",
						regexp.QuoteMeta(set.Name),
					),
				),
				ReplicasStatus: proto.ReplicasStatus{
					Desired:   newInt32Pointer(set.Status.Replicas),
					Ready:     newInt32Pointer(set.Status.ReadyReplicas),
					Available: newInt32Pointer(set.Status.CurrentReplicas),
				},
			})
		}
	}

	if daemonSets != nil {
		for _, daemon := range daemonSets.Items {
			resources = append(resources, Resource{
				Kind:        "DaemonSet",
				Annotations: daemon.Annotations,
				Namespace:   daemon.Namespace,
				Name:        daemon.Name,
				Containers:  daemon.Spec.Template.Spec.Containers,
				PodRegexp: regexp.MustCompile(
					fmt.Sprintf(
						"^%s-[^-]+$",
						regexp.QuoteMeta(daemon.Name),
					),
				),
				ReplicasStatus: proto.ReplicasStatus{
					Desired:   newInt32Pointer(daemon.Status.DesiredNumberScheduled),
					Ready:     newInt32Pointer(daemon.Status.NumberReady),
					Available: newInt32Pointer(daemon.Status.NumberAvailable),
				},
			})
		}
	}

	if replicaSets != nil {
		for _, replicaSet := range replicaSets.Items {
			// skipping when it is a part of another service
			if len(replicaSet.GetOwnerReferences()) > 0 {
				continue
			}
			resources = append(resources, Resource{
				Kind:        "ReplicaSet",
				Annotations: replicaSet.Annotations,
				Namespace:   replicaSet.Namespace,
				Name:        replicaSet.Name,
				Containers:  replicaSet.Spec.Template.Spec.Containers,
				PodRegexp: regexp.MustCompile(
					fmt.Sprintf(
						"^%s-[^-]+$",
						regexp.QuoteMeta(replicaSet.Name),
					),
				),
				ReplicasStatus: proto.ReplicasStatus{
					Desired:   newInt32Pointer(replicaSet.Status.Replicas),
					Ready:     newInt32Pointer(replicaSet.Status.ReadyReplicas),
					Available: newInt32Pointer(replicaSet.Status.AvailableReplicas),
				},
			})
		}
	}

	if cronJobs != nil {
		for _, cronJob := range cronJobs.Items {
			activeCount := int32(len(cronJob.Status.Active))
			resources = append(resources, Resource{
				Kind:        "CronJob",
				Annotations: cronJob.Annotations,
				Namespace:   cronJob.Namespace,
				Name:        cronJob.Name,
				Containers:  cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers,
				PodRegexp: regexp.MustCompile(
					fmt.Sprintf(
						"^%s-[^-]+-[^-]+$",
						regexp.QuoteMeta(cronJob.Name),
					),
				),
				ReplicasStatus: proto.ReplicasStatus{
					Ready: newInt32Pointer(activeCount),
				},
			})
		}
	}

	apps := []*Application{}

	namespaces := map[string]*Application{}

	for _, resource := range resources {
		if utils.InSkipNamespace(scanner.skipNamespaces, resource.Namespace) {
			scanner.client.Tracef(
				nil,
				"skipping namespace %q: resource %q",
				resource.Namespace,
				resource.Name,
			)

			continue
		}

		var app *Application
		var ok bool

		if app, ok = namespaces[resource.Namespace]; !ok {
			// 3d709b8f-d6c5-f629-bdd2-fb0ef6f71539
			app = &Application{
				Entity: Entity{
					Name: resource.Namespace,
				},
				LimitRanges: getLimitRangesForNamespace(
					limitRanges.Items,
					resource.Namespace,
				),
			}

			namespaces[resource.Namespace] = app

			apps = append(apps, app)
		}

		defaultRequests, defaultLimits := getDefaultResources(app.LimitRanges)

		service := &Service{
			Entity: Entity{
				Name:        resource.Name,
				Kind:        resource.Kind,
				Annotations: resource.Annotations,
			},
			ReplicasStatus: resource.ReplicasStatus,

			PodRegexp: resource.PodRegexp,
		}

		// NOTE: we consider the default value is the neutral multiplier `1`
		var replicas int64 = 1
		if resource.ReplicasStatus.Ready != nil {
			// NOTE: we consider the Ready replicas count as it represent the running pods
			// NOTE: that reserve actual resources
			replicas = int64(*resource.ReplicasStatus.Ready)
		}

		for _, container := range resource.Containers {
			resources := withDefaultResources(container.Resources, defaultRequests, defaultLimits)
			resources.ResourceRequirements = applyReplicas(resources.SpecResourceRequirements, replicas)

			service.Containers = append(service.Containers, &Container{
				Entity: Entity{
					Name: container.Name,
				},

				Image:     container.Image,
				Resources: resources,
			})

			scanner.logger.Tracef(
				karma.
					Describe("application", app.Name).
					Describe("service", service.Name),
				"found container %q %q",
				container.Name,
				container.Image,
			)
		}

		app.Services = append(app.Services, service)
	}

	err = identifyApplications(apps, scanner.clusterID)
	if err != nil {
		return nil, nil, karma.Format(
			err,
			"unable to assign UUIDs to scanned applications",
		)
	}

	return apps, rawResources, nil
}

func newInt32Pointer(val int32) *int32 {
	res := new(int32)
	*res = val
	return res
}

func withDefaultResources(
	resources kv1.ResourceRequirements,
	defaultRequests kv1.ResourceList,
	defaultLimits kv1.ResourceList,
) *proto.ContainerResourceRequirements {
	limitsKinds := proto.ResourcesRequirementsKind{}
	requestsKinds := proto.ResourcesRequirementsKind{}
	limits := resources.Limits
	if limits == nil {
		limits = kv1.ResourceList{}
	}
	requests := resources.Requests
	if requests == nil {
		requests = kv1.ResourceList{}
	}
	if !quantityHasValue(requests, kv1.ResourceCPU) {
		if quantityHasValue(limits, kv1.ResourceCPU) {
			limit := limits.Cpu()
			requests[kv1.ResourceCPU] = *limit
			requestsKinds[kv1.ResourceCPU] = proto.ResourceRequirementKindDefaultFromLimits
		} else if quantityHasValue(defaultRequests, kv1.ResourceCPU) {
			request := defaultRequests.Cpu()
			requests[kv1.ResourceCPU] = *request
			requestsKinds[kv1.ResourceCPU] = proto.ResourceRequirementKindDefaultsLimitRange
		}
	} else {
		requestsKinds[kv1.ResourceCPU] = proto.ResourceRequirementKindSet
	}
	if !quantityHasValue(requests, kv1.ResourceMemory) {
		if quantityHasValue(limits, kv1.ResourceMemory) {
			limit := limits.Memory()
			requests[kv1.ResourceMemory] = *limit
			requestsKinds[kv1.ResourceMemory] = proto.ResourceRequirementKindDefaultFromLimits
		} else if quantityHasValue(defaultRequests, kv1.ResourceMemory) {
			request := defaultRequests.Memory()
			requests[kv1.ResourceMemory] = *request
			requestsKinds[kv1.ResourceMemory] = proto.ResourceRequirementKindDefaultsLimitRange
		}
	} else {
		requestsKinds[kv1.ResourceMemory] = proto.ResourceRequirementKindSet
	}

	if !quantityHasValue(limits, kv1.ResourceCPU) {
		if quantityHasValue(defaultLimits, kv1.ResourceCPU) {
			limit := defaultLimits.Cpu()
			limits[kv1.ResourceCPU] = *limit
			limitsKinds[kv1.ResourceCPU] = proto.ResourceRequirementKindDefaultsLimitRange
		}
	} else {
		limitsKinds[kv1.ResourceCPU] = proto.ResourceRequirementKindSet
	}
	if !quantityHasValue(limits, kv1.ResourceMemory) {
		if quantityHasValue(defaultLimits, kv1.ResourceMemory) {
			limit := defaultLimits.Memory()
			limits[kv1.ResourceMemory] = *limit
			limitsKinds[kv1.ResourceMemory] = proto.ResourceRequirementKindDefaultsLimitRange
		}
	} else {
		limitsKinds[kv1.ResourceMemory] = proto.ResourceRequirementKindSet
	}
	return &proto.ContainerResourceRequirements{
		SpecResourceRequirements: kv1.ResourceRequirements{
			Limits:   limits,
			Requests: requests,
		},
		LimitsKinds:   limitsKinds,
		RequestsKinds: requestsKinds,
	}
}

func applyReplicas(resources kv1.ResourceRequirements, replicas int64) kv1.ResourceRequirements {
	requests := kv1.ResourceList{}
	limits := kv1.ResourceList{}

	if cpu, ok := resources.Requests[kv1.ResourceCPU]; ok {
		requests[kv1.ResourceCPU] = multiplyQuantity(cpu, replicas, kresource.Milli)
	}

	if memory, ok := resources.Requests[kv1.ResourceMemory]; ok {
		requests[kv1.ResourceMemory] = multiplyQuantity(memory, replicas, kresource.Scale(0))
	}

	if cpu, ok := resources.Limits[kv1.ResourceCPU]; ok {
		limits[kv1.ResourceCPU] = multiplyQuantity(cpu, replicas, kresource.Milli)
	}

	if memory, ok := resources.Limits[kv1.ResourceMemory]; ok {
		limits[kv1.ResourceMemory] = multiplyQuantity(memory, replicas, kresource.Scale(0))
	}

	return kv1.ResourceRequirements{
		Requests: requests,
		Limits:   limits,
	}
}

func quantityHasValue(resources kv1.ResourceList, resource kv1.ResourceName) bool {
	_, ok := resources[resource]
	return ok
}

func multiplyQuantity(q kresource.Quantity, multiplier int64, scale kresource.Scale) kresource.Quantity {
	mq := q.DeepCopy()
	mq.SetScaled(mq.ScaledValue(scale)*multiplier, scale)
	return mq
}

func getDefaultResources(limitRanges []kv1.LimitRange) (kv1.ResourceList, kv1.ResourceList) {
	defaultRequests, defaultLimits := kv1.ResourceList{}, kv1.ResourceList{}
	for _, limitRange := range limitRanges {
		for _, limitItem := range limitRange.Spec.Limits {
			if limitItem.Type == kv1.LimitTypeContainer {

				if quantityHasValue(limitItem.DefaultRequest, kv1.ResourceCPU) {
					cpu := limitItem.DefaultRequest.Cpu()
					defaultRequests[kv1.ResourceCPU] = *cpu
				}
				if quantityHasValue(limitItem.DefaultRequest, kv1.ResourceMemory) {
					memory := limitItem.DefaultRequest.Memory()
					defaultRequests[kv1.ResourceMemory] = *memory
				}

				if quantityHasValue(limitItem.Default, kv1.ResourceCPU) {
					cpu := limitItem.Default.Cpu()
					defaultLimits[kv1.ResourceCPU] = *cpu
				}

				if quantityHasValue(limitItem.Default, kv1.ResourceMemory) {
					memory := limitItem.Default.Memory()
					defaultLimits[kv1.ResourceMemory] = *memory
				}
			}
		}
	}
	return defaultRequests, defaultLimits
}

// FindService find app and service id from pod name and namespace
func (scanner *Scanner) FindService(
	namespace string,
	podName string,
) (uuid.UUID, uuid.UUID, bool) {
	if utils.InSkipNamespace(scanner.skipNamespaces, namespace) {
		return uuid.Nil, uuid.Nil, false
	}

	scanner.logger.Tracef(
		nil,
		"scanner: find service: %s %s",
		namespace,
		podName,
	)
	scanner.mutex.Lock()
	defer scanner.mutex.Unlock()

	appID, serviceID, found := scanner.history.FindService(
		namespace, podName,
	)
	if !found {
		appID, serviceID, found = scanner.findService(
			scanner.apps, namespace, podName,
		)

		if found {
			scanner.history.PopulateService(
				namespace, podName,
				appID, serviceID,
			)
		}
	}

	return appID, serviceID, found
}

// FindContainer returns app, service id and container from pod namespace,
// name and container name
func (scanner *Scanner) FindContainer(
	namespace string,
	podName string,
	containerName string,
) (uuid.UUID, uuid.UUID, *Container, bool) {
	if utils.InSkipNamespace(scanner.skipNamespaces, namespace) {
		return uuid.Nil, uuid.Nil, nil, false
	}

	scanner.logger.Tracef(
		nil,
		"scanner: find container: %s %s %s",
		namespace,
		podName,
		containerName,
	)
	scanner.mutex.Lock()
	defer scanner.mutex.Unlock()

	appID, serviceID, container, found := scanner.history.FindContainer(
		namespace, podName, containerName,
	)
	if !found {
		appID, serviceID, container, found = scanner.findContainer(
			scanner.apps, namespace, podName, containerName,
		)

		if found {
			scanner.history.PopulateContainer(
				namespace, podName, containerName,
				appID, serviceID, container,
			)
		}
	}

	return appID, serviceID, container, found
}

func (scanner *Scanner) findContainer(
	apps []*Application,
	namespace string,
	podName string,
	containerName string,
) (uuid.UUID, uuid.UUID, *Container, bool) {
	var (
		appID     uuid.UUID
		serviceID uuid.UUID
		container *Container
		found     bool
	)

	for _, app := range apps {
		if app.Name != namespace {
			continue
		}

		appID = app.ID

		for _, service := range app.Services {
			if !service.PodRegexp.MatchString(podName) {
				continue
			}

			serviceID = service.ID

			if containerName != "" {
				for _, searchContainer := range service.Containers {
					if searchContainer.Name != containerName {
						continue
					}

					container = searchContainer

					found = true

					break
				}
			}

			break
		}

		break
	}

	return appID, serviceID, container, found
}

// FindServiceByID returns namespace, name and kind of a service by service id
func (scanner *Scanner) FindServiceByID(
	apps []*Application,
	serviceID uuid.UUID,
) (namespace, name, kind string, found bool) {
	// TODO: optimize
	for _, app := range apps {
		for _, service := range app.Services {
			if service.ID == serviceID {
				found = true
				name = service.Name
				namespace = app.Name
				kind = service.Kind
				return
			}
		}
	}
	return
}

// FindContainerByID returns container, service and application from container id
func (scanner *Scanner) FindContainerByID(
	apps []*Application,
	containerID uuid.UUID,
) (c *Container, s *Service, a *Application, found bool) {
	// TODO: optimize
	for _, app := range apps {
		for _, service := range app.Services {
			for _, container := range service.Containers {
				if container.ID == containerID {
					found = true
					a = app
					s = service
					c = container
					return
				}
			}
		}
	}
	return
}

// FindContainerNameByID returns container name from container id
func (scanner *Scanner) FindContainerNameByID(
	apps []*Application,
	containerID uuid.UUID,
) (name string, found bool) {
	// TODO: optimize
	for _, app := range apps {
		for _, service := range app.Services {
			for _, container := range service.Containers {
				if container.ID == containerID {
					found = true
					name = container.Name
					return
				}
			}
		}
	}
	return
}

func (scanner *Scanner) findService(
	apps []*Application,
	namespace string,
	podName string,
) (uuid.UUID, uuid.UUID, bool) {
	var (
		appID     uuid.UUID
		serviceID uuid.UUID
		found     bool
	)

	for _, app := range apps {
		if app.Name != namespace {
			continue
		}

		appID = app.ID

		for _, service := range app.Services {
			if !service.PodRegexp.MatchString(podName) {
				continue
			}

			serviceID = service.ID

			found = true

			break
		}

		break
	}

	return appID, serviceID, found
}

// FindContainerByPodUIDContainerName find application, service, container id
// and pod name from kubernets pod id and container name
func (scanner *Scanner) FindContainerByPodUIDContainerName(
	podUID string,
	containerName string,
) (
	applicationID uuid.UUID,
	serviceID uuid.UUID,
	containerID uuid.UUID,
	podName string,
	ok bool,
) {
	for _, pod := range scanner.pods {
		if string(pod.UID) == podUID {
			podName = pod.Name
			namespace := pod.Namespace
			cs := pod.Spec.Containers
			if len(cs) == 0 {
				break
			} else {
				if containerName == "" {
					containerName = cs[0].Name
				}
				var container *Container
				applicationID, serviceID, container, ok = scanner.findContainer(
					scanner.apps,
					namespace,
					podName,
					containerName,
				)
				if !ok {
					return
				}
				containerID = container.ID
				return
			}
		}
	}
	return
}

func (scanner *Scanner) NodesLastScanTime() time.Time {
	return scanner.nodesLastScan
}

func (scanner *Scanner) AppsLastScanTime() time.Time {
	return scanner.appsLastScan
}
