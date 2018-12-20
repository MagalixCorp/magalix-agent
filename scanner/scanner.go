package scanner

import (
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
	*utils.Ticker

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
	scanner.Ticker = utils.NewTicker(intervalScanner, scanner.scan)
	scanner.Start(true, false)
	return scanner
}

func (scanner *Scanner) scan() {
	scanner.scanNodes()
	scanner.scanApplications()
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

func (scanner *Scanner) getNodes() ([]kuber.Node, *kv1.NodeList, error) {
	nodeList, err := scanner.kube.GetNodes()
	if err != nil {
		return nil, nil, err
	}

	podList, err := scanner.kube.GetPods()
	if err != nil {
		return nil, nil, err
	}

	nodes := kuber.UpdateNodesContainers(
		kuber.GetNodes(nodeList.Items),
		kuber.GetContainersByNode(podList.Items),
	)

	nodes = kuber.AddContainerListToNodes(
		nodes,
		podList.Items,
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

func (scanner *Scanner) getApplications() (
	[]*Application, map[string]interface{}, error,
) {
	pods, limitRanges, resources, rawResources := scanner.kube.GetResources()
	scanner.pods = pods

	var apps []*Application

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
			app = &Application{
				Entity: Entity{
					Name: resource.Namespace,
				},
				LimitRanges: getLimitRangesForNamespace(
					limitRanges,
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

	err := identifyApplications(apps, scanner.clusterID)
	if err != nil {
		return nil, nil, karma.Format(
			err,
			"unable to assign UUIDs to scanned applications",
		)
	}

	return apps, rawResources, nil
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
