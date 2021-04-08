package kuber

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/client-go/discovery"

	"github.com/MagalixCorp/magalix-agent/v3/proto"
	"github.com/MagalixTechnologies/core/logger"
	v1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	appsV1 "k8s.io/api/apps/v1"
	authv1 "k8s.io/api/authorization/v1"
	kbeta1 "k8s.io/api/batch/v1beta1"
	kv1 "k8s.io/api/core/v1"
	kmeta "k8s.io/apimachinery/pkg/apis/meta/v1"
	kapps "k8s.io/client-go/kubernetes/typed/apps/v1"
	batch "k8s.io/client-go/kubernetes/typed/batch/v1beta1"
	kcore "k8s.io/client-go/kubernetes/typed/core/v1"
	krest "k8s.io/client-go/rest"
)

const (
	milliCore   = 1000
	maskedValue = "**MASKED**"
)

// Kube kube struct
type Kube struct {
	Clientset   *kubernetes.Clientset
	ClientV1    *kapps.AppsV1Client
	ClientBatch *batch.BatchV1beta1Client

	core   kcore.CoreV1Interface
	apps   kapps.AppsV1Interface
	batch  batch.BatchV1beta1Interface
	config *krest.Config
}

// RequestLimit request limit
type RequestLimit struct {
	CPU    *int64
	Memory *int64
}

// ContainerResources container resources
type ContainerResourcesRequirements struct {
	Name     string
	Requests *RequestLimit
	Limits   *RequestLimit
}

type patch struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value"`
}

// TotalResources service resources and replicas
type TotalResources struct {
	Containers []ContainerResourcesRequirements
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

type RawResources struct {
	PodList        *kv1.PodList
	LimitRangeList *kv1.LimitRangeList

	CronJobList *kbeta1.CronJobList

	DeploymentList  *appsV1.DeploymentList
	StatefulSetList *appsV1.StatefulSetList
	DaemonSetList   *appsV1.DaemonSetList
	ReplicaSetList  *appsV1.ReplicaSetList
}

func InitKubernetes(config *krest.Config) (*Kube, error) {

	logger.Debugw(
		"initializing kubernetes Clientset",
		"url", config.Host,
		"token", config.BearerToken,
		"insecure", config.Insecure,
	)

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("unable to create Clientset, error: %w", err)
	}

	clientV1, err := kapps.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("unable to create ClientV1 error: %w", err)
	}

	clientV1Beta1, err := batch.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("unable to create batch clientV1Beta1, error : %w", err)
	}

	kube := &Kube{
		Clientset: clientset,
		ClientV1:  clientV1,
		core:      clientset.CoreV1(),
		apps:      clientset.AppsV1(),
		batch:     clientV1Beta1,
		config:    config,
	}

	return kube, nil
}

// GetNodes get kubernetes nodes
func (kube *Kube) GetNodes(ctx context.Context) (*kv1.NodeList, error) {
	logger.Debugw("retrieving list of nodes")

	nodes, err := kube.core.Nodes().List(ctx, kmeta.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve nodes from all namespaces, error: %w", err)
	}

	return nodes, nil
}

// GetNamespace get namespace by its name
func (kube *Kube) GetNamespace(ctx context.Context, name string) (*kv1.Namespace, error) {
	logger.Debugw("retrieving namespace: %s", name)

	namespace, err := kube.core.Namespaces().Get(ctx, name, kmeta.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to get namespace: %s, error: %w", name, err)
	}
	return namespace, nil
}

// GetPods get kubernetes pods
func (kube *Kube) GetPods(ctx context.Context) (*kv1.PodList, error) {
	logger.Debugw("retrieving list of pods")

	podList, err := kube.core.Pods("").List(ctx, kmeta.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve pods from all namespaces, error: %w", err)
	}

	return podList, nil
}

// GetPods get kubernetes pods for namespace
func (kube *Kube) GetNameSpacePods(ctx context.Context, namespace string) (*kv1.PodList, error) {
	logger.Debug("retrieving list of pods")

	podList, err := kube.core.Pods(namespace).List(ctx, kmeta.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve pods from all namespaces, error: %w", err)
	}

	return podList, nil
}

// GetReplicationControllers get replication controllers
func (kube *Kube) GetReplicationControllers(ctx context.Context) (*kv1.ReplicationControllerList, error) {
	logger.Debug("retrieving list of replication controllers")

	controllers, err := kube.core.ReplicationControllers("").List(ctx, kmeta.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve replication controllers from all namespaces, error: %w", err)
	}

	if controllers != nil {
		for _, item := range controllers.Items {
			maskPodSpec(&item.Spec.Template.Spec)
		}
	}

	return controllers, nil
}

// GetDeployments get deployments
func (kube *Kube) GetDeployments(ctx context.Context) (*appsV1.DeploymentList, error) {
	logger.Debug("retrieving list of deployments")

	deployments, err := kube.apps.Deployments("").List(ctx, kmeta.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve deployments from all namespaces, error: %w", err)
	}

	if deployments != nil {
		for _, item := range deployments.Items {
			maskPodSpec(&item.Spec.Template.Spec)
		}
	}

	return deployments, nil
}

// GetDeployments get deployments
func (kube *Kube) GetDeploymentByName(ctx context.Context, namespace, name string) (*appsV1.Deployment, error) {
	logger.Debug("retrieving list of deployments")

	deployment, err := kube.apps.Deployments(namespace).Get(ctx, name, kmeta.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve deployments from all namespaces, error: %w", err)
	}

	if deployment != nil {
		maskPodSpec(&deployment.Spec.Template.Spec)
	}

	return deployment, nil
}

// GetStatefulSets get stateful sets
func (kube *Kube) GetStatefulSets(ctx context.Context) (*appsV1.StatefulSetList, error) {
	logger.Debug("retrieving list of stateful sets")

	statefulSets, err := kube.apps.StatefulSets("").List(ctx, kmeta.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve stateful sets from all namespaces, error: %w", err)
	}

	if statefulSets != nil {
		for _, item := range statefulSets.Items {
			maskPodSpec(&item.Spec.Template.Spec)
		}
	}

	return statefulSets, nil
}

// GetDaemonSets get daemon sets
func (kube *Kube) GetDaemonSets(ctx context.Context) (*appsV1.DaemonSetList, error) {
	logger.Debug("retrieving list of daemon sets")

	daemonSets, err := kube.apps.DaemonSets("").List(ctx, kmeta.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve daemon sets from all namespaces, error: %w", err)
	}

	if daemonSets != nil {
		for _, item := range daemonSets.Items {
			maskPodSpec(&item.Spec.Template.Spec)
		}
	}

	return daemonSets, nil
}

// GetReplicaSets get replicasets
func (kube *Kube) GetReplicaSets(ctx context.Context) (*appsV1.ReplicaSetList, error) {
	logger.Debug("retrieving list of replica sets")

	replicaSets, err := kube.apps.ReplicaSets("").List(ctx, kmeta.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve replica sets from all namespaces, error: %w", err)
	}

	if replicaSets != nil {
		for _, item := range replicaSets.Items {
			maskPodSpec(&item.Spec.Template.Spec)
		}
	}

	return replicaSets, nil
}

// GetReplicaSets get replicasets
func (kube *Kube) GetNamespaceReplicaSets(ctx context.Context, namespace string) (*appsV1.ReplicaSetList, error) {
	logger.Debug("retrieving list of replica sets")

	replicaSets, err := kube.apps.ReplicaSets(namespace).List(ctx, kmeta.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve replica sets from all namespaces, error: %w", err)
	}

	if replicaSets != nil {
		for _, item := range replicaSets.Items {
			maskPodSpec(&item.Spec.Template.Spec)
		}
	}

	return replicaSets, nil
}

// GetCronJobs get cron jobs
func (kube *Kube) GetCronJobs(ctx context.Context) (*kbeta1.CronJobList, error) {
	logger.Debug("retrieving list of cron jobs")

	cronJobs, err := kube.batch.CronJobs("").List(ctx, kmeta.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve cron jobs from all namespaces, error: %w", err)
	}

	if cronJobs != nil {
		for _, item := range cronJobs.Items {
			maskPodSpec(&item.Spec.JobTemplate.Spec.Template.Spec)
		}
	}

	return cronJobs, nil
}

// GetCronJobs get cron jobs
func (kube *Kube) GetCronJob(ctx context.Context, namespace, name string) (*kbeta1.CronJob, error) {
	logger.Debug("retrieving list of cron jobs")

	cronJob, err := kube.batch.CronJobs(namespace).Get(ctx, name, kmeta.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve cron jobs from all namespaces, error: %w", err)
	}

	if cronJob != nil {
		maskPodSpec(&cronJob.Spec.JobTemplate.Spec.Template.Spec)
	}

	return cronJob, nil
}

// GetLimitRanges get limits and ranges for namespaces
func (kube *Kube) GetLimitRanges(ctx context.Context) (*kv1.LimitRangeList, error) {
	logger.Debug("retrieving list of limitRanges from all namespaces")

	limitRanges, err := kube.core.LimitRanges("").List(ctx, kmeta.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve list of limitRanges from all namespaces, error: %w", err)
	}

	return limitRanges, nil
}

func (kube *Kube) GetStatefulSet(ctx context.Context, namespace, name string) (*v1.StatefulSet, error) {
	logger.Debug("retrieving statefulset by its name")

	statefulSet, err := kube.Clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, kmeta.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve statefulset %s/%s, error: %w", namespace, name, err)
	}

	if statefulSet != nil {
		maskPodSpec(&statefulSet.Spec.Template.Spec)
	}

	return statefulSet, nil
}

func (kube *Kube) GetDaemonSet(ctx context.Context, namespace, name string) (*v1.DaemonSet, error) {
	logger.Debug("retrieving daemonset by its name")

	daemonSet, err := kube.Clientset.AppsV1().DaemonSets(namespace).Get(ctx, name, kmeta.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve daemonSet %s/%s, error: %w", namespace, name, err)
	}

	if daemonSet != nil {
		maskPodSpec(&daemonSet.Spec.Template.Spec)
	}

	return daemonSet, nil
}

// SetResources set resources for a service
func (kube *Kube) SetResources(ctx context.Context, kind string, name string, namespace string, totalResources TotalResources) (skipped bool, err error) {
	if len(totalResources.Containers) == 0 {
		return false, fmt.Errorf("invalid resources passed, nothing to change")
	}

	if strings.ToLower(kind) == "statefulset" {
		statefulSet, err := kube.GetStatefulSet(ctx, namespace, name)
		if err != nil {
			return false, fmt.Errorf("unable to get sts definition, error: %w", err)
		}

		updateStrategy := statefulSet.Spec.UpdateStrategy.Type

		errMap := map[string]interface{}{
			"replicas":        *statefulSet.Spec.Replicas,
			"update-strategy": updateStrategy,
		}

		if updateStrategy == v1.RollingUpdateStatefulSetStrategyType {

			// no rollingUpdate spec, then Partition = 0
			if statefulSet.Spec.UpdateStrategy.RollingUpdate != nil {
				partition := statefulSet.Spec.UpdateStrategy.RollingUpdate.Partition
				errMap["rolling-update-partition"] = partition

				if partition != nil && *partition != 0 {
					return true, fmt.Errorf(
						"Spec.UpdateStrategy.RollingUpdate.Partition not equal 0 with data: %+v",
						errMap,
					)
				}
			}

		} else {
			return true, fmt.Errorf(
				"Spec.UpdateStrategy not equal 'RollingUpdate' with data: %+v", errMap,
			)
		}
	}

	var containerSpecs = make([]map[string]interface{}, len(totalResources.Containers))
	for i := range totalResources.Containers {

		container := totalResources.Containers[i]
		resources := map[string]map[string]interface{}{
			"limits":   {},
			"requests": {},
		}

		if container.Limits.Memory != nil {
			memoryLimits := fmt.Sprintf("%dMi", *container.Limits.Memory)
			resources["limits"]["memory"] = memoryLimits
		}
		if container.Limits.CPU != nil {
			cpuLimits := float64(*container.Limits.CPU) / milliCore
			resources["limits"]["cpu"] = cpuLimits
		}

		if container.Requests.Memory != nil {
			memoryRequests := fmt.Sprintf("%dMi", *container.Requests.Memory)
			resources["requests"]["memory"] = memoryRequests
		}
		if container.Requests.CPU != nil {
			cpuRequests := float64(*container.Requests.CPU) / milliCore
			resources["requests"]["cpu"] = cpuRequests
		}

		if len(resources) == 0 {
			return false, fmt.Errorf(
				"invalid resources for container: %s",
				container.Name,
			)
		}

		spec := map[string]interface{}{
			"name":      container.Name,
			"resources": resources,
		}
		containerSpecs[i] = spec
	}

	body := map[string]interface{}{
		"kind": kind,
		"spec": map[string]interface{}{},
	}

	if len(containerSpecs) > 0 {
		spec := body["spec"].(map[string]interface{})
		spec["template"] = map[string]interface{}{
			"spec": map[string]interface{}{
				"containers": containerSpecs,
			},
		}
	}

	b, err := json.Marshal(body)
	if err != nil {
		return false, err
	}
	req := kube.ClientV1.RESTClient().Patch(types.StrategicMergePatchType).
		Resource(kind + "s").
		Namespace(namespace).
		Name(name).
		Body(bytes.NewBuffer(b))

	res := req.Do(context.Background())

	_, err = res.Get()
	return false, err
}

func (kube *Kube) GetServerVersion() (string, error) {
	discoveryClient := discovery.NewDiscoveryClient(kube.Clientset.CoreV1().RESTClient())
	version, err := discoveryClient.ServerVersion()
	if err != nil {
		return "", err
	}

	return version.String(), nil
}

func (kube *Kube) GetServerMinorVersion() (int, error) {
	discoveryClient := discovery.NewDiscoveryClient(kube.Clientset.CoreV1().RESTClient())
	version, err := discoveryClient.ServerVersion()
	if err != nil {
		return 0, err
	}

	minor := version.Minor

	// remove + if found contains
	last1 := minor[len(minor)-1:]
	if last1 == "+" {
		minor = minor[0 : len(minor)-1]
	}

	return strconv.Atoi(minor)
}

func (kube *Kube) GetAgentPermissions(ctx context.Context) (string, error) {
	logger.Debug("getting agent permissions")

	rulesSpec := authv1.SelfSubjectRulesReview{
		Spec: authv1.SelfSubjectRulesReviewSpec{
			Namespace: "kube-system",
		},
		Status: authv1.SubjectRulesReviewStatus{
			Incomplete: false,
		},
	}

	subjectRules, err := kube.Clientset.AuthorizationV1().SelfSubjectRulesReviews().Create(ctx, &rulesSpec, kmeta.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("unable to get agent permissions, error: %w", err)
	}

	rules, _ := json.Marshal(subjectRules.Status.ResourceRules)
	return string(rules), nil
}

func (kube *Kube) UpdateValidatingWebhookCaBundle(ctx context.Context, name string, certPem []byte) error {
	logger.Debug("updating web hook's client ca bundle")

	payload := []patch{{
		Op:    "replace",
		Path:  "/webhooks/0/clientConfig/caBundle",
		Value: base64.StdEncoding.EncodeToString(certPem),
	}}

	payloadBytes, _ := json.Marshal(payload)
	_, err := kube.Clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().Patch(
		ctx,
		name,
		types.JSONPatchType,
		payloadBytes,
		kmeta.PatchOptions{},
	)

	if err != nil {
		return fmt.Errorf("Unable to update web hook's client ca bundle, error: %w", err)
	}
	return nil
}

// func (kube *Kube) GetResources() (
// 	pods []kv1.Pod,
// 	limitRanges []kv1.LimitRange,
// 	resources []Resource,
// 	rawResources map[string]interface{},
// 	err error,
// ) {
// 	rawResources = map[string]interface{}{}

// 	m := sync.Mutex{}
// 	group := errgroup.Group{}

// 	group.Go(func() error {
// 		controllers, err := kube.GetReplicationControllers()
// 		if err != nil {
// 			return fmt.Errorf(
// 				"unable to get replication controllers, error: %w",
// 				err,
// 			)
// 		}

// 		if controllers != nil {
// 			m.Lock()
// 			defer m.Unlock()

// 			rawResources["controllers"] = controllers

// 			for _, controller := range controllers.Items {
// 				resources = append(resources, Resource{
// 					Kind:        "ReplicationController",
// 					Annotations: controller.Annotations,
// 					Namespace:   controller.Namespace,
// 					Name:        controller.Name,
// 					Containers:  controller.Spec.Template.Spec.Containers,
// 					PodRegexp: regexp.MustCompile(
// 						fmt.Sprintf(
// 							"^%s-[^-]+$",
// 							regexp.QuoteMeta(controller.Name),
// 						),
// 					),
// 					ReplicasStatus: proto.ReplicasStatus{
// 						Desired:   controller.Spec.Replicas,
// 						Current:   newInt32Pointer(controller.Status.Replicas),
// 						Ready:     newInt32Pointer(controller.Status.ReadyReplicas),
// 						Available: newInt32Pointer(controller.Status.AvailableReplicas),
// 					},
// 				})
// 			}
// 		}
// 		return nil
// 	})

// 	group.Go(func() error {
// 		podList, err := kube.GetPods()
// 		if err != nil {
// 			return fmt.Errorf(
// 				"unable to get pods, error: %w",
// 				err,
// 			)
// 		}

// 		if podList != nil {
// 			pods = podList.Items

// 			m.Lock()
// 			defer m.Unlock()

// 			rawResources["pods"] = podList

// 			for _, pod := range pods {
// 				if len(pod.OwnerReferences) > 0 {
// 					continue
// 				}
// 				resources = append(resources, Resource{
// 					Kind:        "OrphanPod",
// 					Annotations: pod.Annotations,
// 					Namespace:   pod.Namespace,
// 					Name:        pod.Name,
// 					Containers:  pod.Spec.Containers,
// 					PodRegexp: regexp.MustCompile(
// 						fmt.Sprintf(
// 							"^%s$",
// 							regexp.QuoteMeta(pod.Name),
// 						),
// 					),
// 					ReplicasStatus: proto.ReplicasStatus{
// 						Desired:   newInt32Pointer(1),
// 						Current:   newInt32Pointer(1),
// 						Ready:     newInt32Pointer(1),
// 						Available: newInt32Pointer(1),
// 					},
// 				})
// 			}
// 		}

// 		return nil
// 	})

// 	group.Go(func() error {
// 		deployments, err := kube.GetDeployments()
// 		if err != nil {
// 			return fmt.Errorf(
// 				"unable to get deployments, error: %w",
// 				err,
// 			)
// 		}

// 		if deployments != nil {
// 			m.Lock()
// 			defer m.Unlock()

// 			rawResources["deployments"] = deployments

// 			for _, deployment := range deployments.Items {
// 				resources = append(resources, Resource{
// 					Kind:        "Deployment",
// 					Annotations: deployment.Annotations,
// 					Namespace:   deployment.Namespace,
// 					Name:        deployment.Name,
// 					Containers:  deployment.Spec.Template.Spec.Containers,
// 					PodRegexp: regexp.MustCompile(
// 						fmt.Sprintf(
// 							"^%s-[^-]+-[^-]+$",
// 							regexp.QuoteMeta(deployment.Name),
// 						),
// 					),
// 					ReplicasStatus: proto.ReplicasStatus{
// 						Desired:   deployment.Spec.Replicas,
// 						Current:   newInt32Pointer(deployment.Status.Replicas),
// 						Ready:     newInt32Pointer(deployment.Status.ReadyReplicas),
// 						Available: newInt32Pointer(deployment.Status.AvailableReplicas),
// 					},
// 				})
// 			}
// 		}

// 		return nil
// 	})

// 	group.Go(func() error {
// 		statefulSets, err := kube.GetStatefulSets()
// 		if err != nil {
// 			return fmt.Errorf(
// 				"unable to get statefulSets, error: %w",
// 				err,
// 			)
// 		}

// 		if statefulSets != nil {
// 			m.Lock()
// 			defer m.Unlock()

// 			rawResources["statefulSets"] = statefulSets

// 			for _, set := range statefulSets.Items {
// 				resources = append(resources, Resource{
// 					Kind:        "StatefulSet",
// 					Annotations: set.Annotations,
// 					Namespace:   set.Namespace,
// 					Name:        set.Name,
// 					Containers:  set.Spec.Template.Spec.Containers,
// 					PodRegexp: regexp.MustCompile(
// 						fmt.Sprintf(
// 							"^%s-([0-9]+)$",
// 							regexp.QuoteMeta(set.Name),
// 						),
// 					),
// 					ReplicasStatus: proto.ReplicasStatus{
// 						Desired:   set.Spec.Replicas,
// 						Current:   newInt32Pointer(set.Status.Replicas),
// 						Ready:     newInt32Pointer(set.Status.ReadyReplicas),
// 						Available: newInt32Pointer(set.Status.CurrentReplicas),
// 					},
// 				})
// 			}
// 		}

// 		return nil
// 	})

// 	group.Go(func() error {
// 		daemonSets, err := kube.GetDaemonSets()
// 		if err != nil {
// 			return fmt.Errorf(
// 				"unable to get daemonSets, error: %w",
// 				err,
// 			)
// 		}

// 		if daemonSets != nil {
// 			m.Lock()
// 			defer m.Unlock()

// 			rawResources["daemonSets"] = daemonSets

// 			for _, daemon := range daemonSets.Items {
// 				resources = append(resources, Resource{
// 					Kind:        "DaemonSet",
// 					Annotations: daemon.Annotations,
// 					Namespace:   daemon.Namespace,
// 					Name:        daemon.Name,
// 					Containers:  daemon.Spec.Template.Spec.Containers,
// 					PodRegexp: regexp.MustCompile(
// 						fmt.Sprintf(
// 							"^%s-[^-]+$",
// 							regexp.QuoteMeta(daemon.Name),
// 						),
// 					),
// 					ReplicasStatus: proto.ReplicasStatus{
// 						Desired:   newInt32Pointer(daemon.Status.DesiredNumberScheduled),
// 						Current:   newInt32Pointer(daemon.Status.CurrentNumberScheduled),
// 						Ready:     newInt32Pointer(daemon.Status.NumberReady),
// 						Available: newInt32Pointer(daemon.Status.NumberAvailable),
// 					},
// 				})
// 			}
// 		}

// 		return nil
// 	})

// 	group.Go(func() error {
// 		replicaSets, err := kube.GetReplicaSets()
// 		if err != nil {
// 			return fmt.Errorf(
// 				"unable to get replicasets, error : %w",
// 				err,
// 			)
// 		}

// 		if replicaSets != nil {
// 			m.Lock()
// 			defer m.Unlock()

// 			rawResources["replicaSets"] = replicaSets

// 			for _, replicaSet := range replicaSets.Items {
// 				// skipping when it is a part of another service
// 				if len(replicaSet.GetOwnerReferences()) > 0 {
// 					continue
// 				}
// 				resources = append(resources, Resource{
// 					Kind:        "ReplicaSet",
// 					Annotations: replicaSet.Annotations,
// 					Namespace:   replicaSet.Namespace,
// 					Name:        replicaSet.Name,
// 					Containers:  replicaSet.Spec.Template.Spec.Containers,
// 					PodRegexp: regexp.MustCompile(
// 						fmt.Sprintf(
// 							"^%s-[^-]+$",
// 							regexp.QuoteMeta(replicaSet.Name),
// 						),
// 					),
// 					ReplicasStatus: proto.ReplicasStatus{
// 						Desired:   replicaSet.Spec.Replicas,
// 						Current:   newInt32Pointer(replicaSet.Status.Replicas),
// 						Ready:     newInt32Pointer(replicaSet.Status.ReadyReplicas),
// 						Available: newInt32Pointer(replicaSet.Status.AvailableReplicas),
// 					},
// 				})
// 			}
// 		}

// 		return nil
// 	})

// 	group.Go(func() error {
// 		cronJobs, err := kube.GetCronJobs()
// 		if err != nil {
// 			return fmt.Errorf(
// 				"unable to get cron jobs, error: %w",
// 				err,
// 			)
// 		}

// 		if cronJobs != nil {
// 			m.Lock()
// 			defer m.Unlock()

// 			rawResources["cronJobs"] = cronJobs

// 			for _, cronJob := range cronJobs.Items {
// 				activeCount := int32(len(cronJob.Status.Active))
// 				resources = append(resources, Resource{
// 					Kind:        "CronJob",
// 					Annotations: cronJob.Annotations,
// 					Namespace:   cronJob.Namespace,
// 					Name:        cronJob.Name,
// 					Containers:  cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers,
// 					PodRegexp: regexp.MustCompile(
// 						fmt.Sprintf(
// 							"^%s-[^-]+-[^-]+$",
// 							regexp.QuoteMeta(cronJob.Name),
// 						),
// 					),
// 					ReplicasStatus: proto.ReplicasStatus{
// 						Current: newInt32Pointer(activeCount),
// 					},
// 				})
// 			}
// 		}

// 		return nil
// 	})

// 	group.Go(func() error {
// 		limitRangeList, err := kube.GetLimitRanges()
// 		if err != nil {
// 			return fmt.Errorf(
// 				"unable to get limitRanges, error: %w",
// 				err,
// 			)
// 		}

// 		if limitRangeList != nil {
// 			limitRanges = limitRangeList.Items

// 			m.Lock()
// 			defer m.Unlock()

// 			rawResources["limitRanges"] = limitRangeList
// 		}

// 		return nil
// 	})

// 	err = group.Wait()

// 	return
// }

// func newInt32Pointer(val int32) *int32 {
// 	res := new(int32)
// 	*res = val
// 	return res
// }
