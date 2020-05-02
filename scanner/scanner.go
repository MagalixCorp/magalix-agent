package scanner

import (
	"sync"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/client"
	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixTechnologies/log-go"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"

	corev1 "k8s.io/api/core/v1"
	kresource "k8s.io/apimachinery/pkg/api/resource"
)

const (
	timeoutScannerBackoff = time.Second * 5
	intervalScanner       = time.Minute * 1
)

type ResourcesProvider interface {
	GetNodes() (*corev1.NodeList, error)
	GetPods() (*corev1.PodList, error)
	GetResources() (
		pods []corev1.Pod,
		limitRanges []corev1.LimitRange,
		resources []kuber.Resource,
		rawResources map[string]interface{},
		err error,
	)
}

// Scanner cluster scanner
type Scanner struct {
	*utils.Ticker

	client            *client.Client
	logger            *log.Logger
	resourcesProvider ResourcesProvider
	skipNamespaces    []string
	accountID         uuid.UUID
	clusterID         uuid.UUID

	apps         []*Application
	appsLastScan time.Time

	pods []corev1.Pod

	nodes         []kuber.Node
	nodeList      []corev1.Node
	nodesLastScan time.Time

	history History
	mutex   *sync.Mutex

	optInAnalysisData  bool
	analysisDataSender func(args ...interface{})

	dones []chan struct{}

	enableSender bool
}

// deprecated: please use entities/EntitiesWatcher instead
// InitScanner creates a new scanner then Start it
func InitScanner(
	client *client.Client,
	resourcesProvider ResourcesProvider,
	skipNamespaces []string,
	accountID uuid.UUID,
	clusterID uuid.UUID,
	optInAnalysisData bool,
	analysisDataInterval time.Duration,
	enableSender bool,
) *Scanner {
	scanner := &Scanner{
		client:            client,
		logger:            client.Logger,
		resourcesProvider: resourcesProvider,
		skipNamespaces:    skipNamespaces,
		accountID:         accountID,
		clusterID:         clusterID,
		history:           NewHistory(),

		optInAnalysisData: optInAnalysisData,

		mutex: &sync.Mutex{},
		dones: make([]chan struct{}, 0),

		enableSender: enableSender,
	}
	if optInAnalysisData {
		scanner.analysisDataSender = utils.Throttle(
			"analysis-data",
			analysisDataInterval,
			2, // we call analysisDataSender twice in each tick
			func(args ...interface{}) {
				if data, ok := args[0].(map[string]interface{}); ok {
					go scanner.client.SendRaw(data)
				} else {
					scanner.logger.Error(
						"invalid raw data type! Please contact developer",
					)
				}
			},
		)
	} else {
		// noop function
		scanner.analysisDataSender = func(args ...interface{}) {}
	}
	scanner.Ticker = utils.NewTicker("scanner", intervalScanner, func(_ time.Time) {
		scanner.scan()
	})
	// Note: we set immediate to true so that the scanner blocks for the first
	// run. Other components depends on scanner having a history to function correctly.
	// The other solution is to let the dependent components to wait for scanner
	// ticks which will make code more coupled and complex.
	scanner.Start(true, false, false)
	return scanner
}

func (scanner *Scanner) scan() {
	wg := sync.WaitGroup{}
	wg.Add(2)
	go func() {
		scanner.scanNodes()
		wg.Done()
	}()
	go func() {
		scanner.scanApplications()
		wg.Done()
	}()
	wg.Wait()
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
		scanner.nodeList = nodeList.Items
		scanner.nodesLastScan = time.Now().UTC()

		scanner.SendNodes(nodes)
		scanner.SendAnalysisData(map[string]interface{}{
			"nodes": nodeList,
		})

		scanner.logger.Infof(
			nil,
			"nodes sent",
		)
		break
	}
}

func (scanner *Scanner) getNodes() ([]kuber.Node, *corev1.NodeList, error) {
	nodeList, err := scanner.resourcesProvider.GetNodes()
	if err != nil {
		return nil, nil, err
	}

	podList, err := scanner.resourcesProvider.GetPods()
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
		scanner.appsLastScan = time.Now().UTC()

		scanner.SendApplications(apps)
		scanner.SendAnalysisData(rawResources)

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
	pods, limitRanges, resources, rawResources, err := scanner.resourcesProvider.GetResources()
	if err != nil {
		return nil, nil, karma.Format(
			err,
			"can't get kube resources",
		)
	}

	scanner.mutex.Lock()
	scanner.pods = pods
	scanner.mutex.Unlock()

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
		if resource.ReplicasStatus.Current != nil {
			// NOTE: we consider the Current replicas count as it represent the running pods
			// NOTE: that reserve actual resources
			replicas = int64(*resource.ReplicasStatus.Current)
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

				LivenessProbe:  container.LivenessProbe,
				ReadinessProbe: container.ReadinessProbe,
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

// getLimitRangesForNamespace returns all LimitRanges for a specific namespace.
func getLimitRangesForNamespace(
	limitRanges []corev1.LimitRange,
	namespace string,
) []corev1.LimitRange {
	var ranges []corev1.LimitRange

	for index, limit := range limitRanges {
		if limit.GetNamespace() == namespace {
			ranges = append(ranges, limitRanges[index])
		}
	}

	return ranges
}

func withDefaultResources(
	resources corev1.ResourceRequirements,
	defaultRequests corev1.ResourceList,
	defaultLimits corev1.ResourceList,
) *proto.ContainerResourceRequirements {
	limitsKinds := proto.ResourcesRequirementsKind{}
	requestsKinds := proto.ResourcesRequirementsKind{}
	limits := resources.Limits
	if limits == nil {
		limits = corev1.ResourceList{}
	}
	requests := resources.Requests
	if requests == nil {
		requests = corev1.ResourceList{}
	}
	if !quantityHasValue(requests, corev1.ResourceCPU) {
		if quantityHasValue(limits, corev1.ResourceCPU) {
			limit := limits.Cpu()
			requests[corev1.ResourceCPU] = *limit
			requestsKinds[corev1.ResourceCPU] = proto.ResourceRequirementKindDefaultFromLimits
		} else if quantityHasValue(defaultRequests, corev1.ResourceCPU) {
			request := defaultRequests.Cpu()
			requests[corev1.ResourceCPU] = *request
			requestsKinds[corev1.ResourceCPU] = proto.ResourceRequirementKindDefaultsLimitRange
		}
	} else {
		requestsKinds[corev1.ResourceCPU] = proto.ResourceRequirementKindSet
	}
	if !quantityHasValue(requests, corev1.ResourceMemory) {
		if quantityHasValue(limits, corev1.ResourceMemory) {
			limit := limits.Memory()
			requests[corev1.ResourceMemory] = *limit
			requestsKinds[corev1.ResourceMemory] = proto.ResourceRequirementKindDefaultFromLimits
		} else if quantityHasValue(defaultRequests, corev1.ResourceMemory) {
			request := defaultRequests.Memory()
			requests[corev1.ResourceMemory] = *request
			requestsKinds[corev1.ResourceMemory] = proto.ResourceRequirementKindDefaultsLimitRange
		}
	} else {
		requestsKinds[corev1.ResourceMemory] = proto.ResourceRequirementKindSet
	}

	if !quantityHasValue(limits, corev1.ResourceCPU) {
		if quantityHasValue(defaultLimits, corev1.ResourceCPU) {
			limit := defaultLimits.Cpu()
			limits[corev1.ResourceCPU] = *limit
			limitsKinds[corev1.ResourceCPU] = proto.ResourceRequirementKindDefaultsLimitRange
		}
	} else {
		limitsKinds[corev1.ResourceCPU] = proto.ResourceRequirementKindSet
	}
	if !quantityHasValue(limits, corev1.ResourceMemory) {
		if quantityHasValue(defaultLimits, corev1.ResourceMemory) {
			limit := defaultLimits.Memory()
			limits[corev1.ResourceMemory] = *limit
			limitsKinds[corev1.ResourceMemory] = proto.ResourceRequirementKindDefaultsLimitRange
		}
	} else {
		limitsKinds[corev1.ResourceMemory] = proto.ResourceRequirementKindSet
	}
	return &proto.ContainerResourceRequirements{
		SpecResourceRequirements: corev1.ResourceRequirements{
			Limits:   limits,
			Requests: requests,
		},
		LimitsKinds:   limitsKinds,
		RequestsKinds: requestsKinds,
	}
}

func applyReplicas(resources corev1.ResourceRequirements, replicas int64) corev1.ResourceRequirements {
	requests := corev1.ResourceList{}
	limits := corev1.ResourceList{}

	if cpu, ok := resources.Requests[corev1.ResourceCPU]; ok {
		requests[corev1.ResourceCPU] = multiplyQuantity(cpu, replicas, kresource.Milli)
	}

	if memory, ok := resources.Requests[corev1.ResourceMemory]; ok {
		requests[corev1.ResourceMemory] = multiplyQuantity(memory, replicas, kresource.Scale(0))
	}

	if cpu, ok := resources.Limits[corev1.ResourceCPU]; ok {
		limits[corev1.ResourceCPU] = multiplyQuantity(cpu, replicas, kresource.Milli)
	}

	if memory, ok := resources.Limits[corev1.ResourceMemory]; ok {
		limits[corev1.ResourceMemory] = multiplyQuantity(memory, replicas, kresource.Scale(0))
	}

	return corev1.ResourceRequirements{
		Requests: requests,
		Limits:   limits,
	}
}

func quantityHasValue(resources corev1.ResourceList, resource corev1.ResourceName) bool {
	_, ok := resources[resource]
	return ok
}

func multiplyQuantity(q kresource.Quantity, multiplier int64, scale kresource.Scale) kresource.Quantity {
	mq := q.DeepCopy()
	mq.SetScaled(mq.ScaledValue(scale)*multiplier, scale)
	return mq
}

func getDefaultResources(limitRanges []corev1.LimitRange) (corev1.ResourceList, corev1.ResourceList) {
	defaultRequests, defaultLimits := corev1.ResourceList{}, corev1.ResourceList{}
	for _, limitRange := range limitRanges {
		for _, limitItem := range limitRange.Spec.Limits {
			if limitItem.Type == corev1.LimitTypeContainer {

				if quantityHasValue(limitItem.DefaultRequest, corev1.ResourceCPU) {
					cpu := limitItem.DefaultRequest.Cpu()
					defaultRequests[corev1.ResourceCPU] = *cpu
				}
				if quantityHasValue(limitItem.DefaultRequest, corev1.ResourceMemory) {
					memory := limitItem.DefaultRequest.Memory()
					defaultRequests[corev1.ResourceMemory] = *memory
				}

				if quantityHasValue(limitItem.Default, corev1.ResourceCPU) {
					cpu := limitItem.Default.Cpu()
					defaultLimits[corev1.ResourceCPU] = *cpu
				}

				if quantityHasValue(limitItem.Default, corev1.ResourceMemory) {
					memory := limitItem.Default.Memory()
					defaultLimits[corev1.ResourceMemory] = *memory
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
func (scanner *Scanner) GetNodes() ([]corev1.Node, error) {
	scanner.mutex.Lock()
	defer scanner.mutex.Unlock()

	nodeList := make([]corev1.Node, len(scanner.nodeList))
	copy(nodeList, scanner.nodeList)

	return nodeList, nil
}

// GetPods get scanned pods
func (scanner *Scanner) GetPods() ([]corev1.Pod, error) {
	scanner.mutex.Lock()
	defer scanner.mutex.Unlock()

	pods := make([]corev1.Pod, len(scanner.pods))
	copy(pods, scanner.pods)

	return pods, nil
}

func (scanner *Scanner) FindController(namespaceName string, podName string) (string, string, error) {
	scanner.mutex.Lock()
	defer scanner.mutex.Unlock()

	for _, namespace := range scanner.apps {
		if namespace.Name != namespaceName {
			continue
		}

		for _, controller := range namespace.Services {
			if controller.PodRegexp.MatchString(podName) {
				return controller.Name, controller.Kind, nil
			}
		}
	}

	return "", "", karma.
		Describe("namespace_name", namespaceName).
		Describe("pod_name", podName).
		Format(nil, "unable to find controller")
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

// FindContainerByID returns container, service and application from container name
func (scanner *Scanner) FindContainerWithParents(
	namespace string,
	podName string,
	containerName string,
) (c *Container, s *Service, a *Application, found bool) {
	scanner.mutex.Lock()
	defer scanner.mutex.Unlock()

	for _, app := range scanner.apps {
		if app.Name != namespace {
			continue
		}

		for _, service := range app.Services {
			if !service.PodRegexp.MatchString(podName) {
				continue
			}

			for _, searchContainer := range service.Containers {
				if searchContainer.Name != containerName {
					continue
				}

				a = app
				s = service
				c = searchContainer

				found = true

				break
			}

			break
		}

		break
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
