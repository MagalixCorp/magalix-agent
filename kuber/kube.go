package kuber

import (
	"bytes"
	"encoding/json"
	"fmt"

	"os"
	"regexp"

	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
	kbeta2 "k8s.io/api/apps/v1beta2"
	kbeta1 "k8s.io/api/batch/v1beta1"
	kv1 "k8s.io/api/core/v1"
	kmeta "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	beta2client "k8s.io/client-go/kubernetes/typed/apps/v1beta2"
	kapps "k8s.io/client-go/kubernetes/typed/apps/v1beta2"
	batch "k8s.io/client-go/kubernetes/typed/batch/v1beta1"
	kcore "k8s.io/client-go/kubernetes/typed/core/v1"
	krest "k8s.io/client-go/rest"
	certutil "k8s.io/client-go/util/cert"

	"github.com/MagalixCorp/magalix-agent/client"
	"github.com/MagalixCorp/magalix-agent/proto"
)

const (
	milliCore   = 1000
	maskedValue = "**MASKED**"
)

// Kube kube struct
type Kube struct {
	Clientset     *kubernetes.Clientset
	ClientV1Beta2 *beta2client.AppsV1beta2Client
	ClientBatch   *batch.BatchV1beta1Client

	core   kcore.CoreV1Interface
	apps   kapps.AppsV1beta2Interface
	batch  batch.BatchV1beta1Interface
	config *krest.Config
	logger *log.Logger
}

// RequestLimit request limit
type RequestLimit struct {
	CPU    *int64
	Memory *int64
}

// ContainerResources container resources
type ContainerResourcesRequirements struct {
	Name     string
	Requests RequestLimit
	Limits   RequestLimit
}

// TotalResources service resources and replicas
type TotalResources struct {
	Replicas   *int
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

	DeploymentList  *kbeta2.DeploymentList
	StatefulSetList *kbeta2.StatefulSetList
	DaemonSetList   *kbeta2.DaemonSetList
	ReplicaSetList  *kbeta2.ReplicaSetList
}

func InitKubernetes(
	args map[string]interface{},
	client *client.Client,
) (*Kube, error) {
	var config *krest.Config
	var err error

	if args["--kube-incluster"].(bool) {
		client.Infof(nil, "initializing kubernetes incluster config")

		config, err = krest.InClusterConfig()
		if err != nil {
			return nil, karma.Format(
				err,
				"unable to get incluster config",
			)
		}

	} else {
		client.Infof(
			nil,
			"initializing kubernetes user-defined config",
		)

		token, _ := args["--kube-token"].(string)
		if token == "" {
			token = os.Getenv("KUBE_TOKEN")
		}

		config = &krest.Config{}
		config.ContentType = kruntime.ContentTypeJSON
		config.APIPath = "/api"
		config.Host = args["--kube-url"].(string)
		config.BearerToken = token

		{
			tlsClientConfig := krest.TLSClientConfig{}
			rootCAFile, ok := args["--kube-root-ca-cert"].(string)
			if ok {
				if _, err := certutil.NewPool(rootCAFile); err != nil {
					fmt.Printf("Expected to load root CA config from %s, but got err: %v", rootCAFile, err)
				} else {
					tlsClientConfig.CAFile = rootCAFile
				}
				config.TLSClientConfig = tlsClientConfig
			}
		}

		if args["--kube-insecure"].(bool) {
			config.Insecure = true
		}
	}

	client.Debugf(
		karma.
			Describe("url", config.Host).
			Describe("token", config.BearerToken).
			Describe("insecure", config.Insecure),
		"initializing kubernetes Clientset",
	)

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to create Clientset",
		)
	}

	clientV1Beta2, err := beta2client.NewForConfig(config)
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to create ClientV1Beta2",
		)
	}

	clientV1Beta1, err := batch.NewForConfig(config)
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to create batch clientV1Beta1",
		)
	}

	kube := &Kube{
		Clientset:     clientset,
		ClientV1Beta2: clientV1Beta2,
		core:          clientset.CoreV1(),
		apps:          clientset.AppsV1beta2(),
		batch:         clientV1Beta1,
		config:        config,
		logger:        client.Logger,
	}

	return kube, nil
}

// GetNodes get kubernetes nodes
func (kube *Kube) GetNodes() (*kv1.NodeList, error) {
	kube.logger.Debugf(nil, "{kubernetes} retrieving list of nodes")
	nodes, err := kube.core.Nodes().List(kmeta.ListOptions{})
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to retrieve nodes from all namespaces",
		)
	}

	return nodes, nil
}

func (kube *Kube) GetResources() (
	pods []kv1.Pod,
	limitRanges []kv1.LimitRange,
	resources []Resource,
	rawResources map[string]interface{},
) {
	controllers, err := kube.GetReplicationControllers()
	if err != nil {
		kube.logger.Errorf(
			err,
			"unable to get replication controllers",
		)
	}

	podList, err := kube.GetPods()
	if err != nil {
		kube.logger.Errorf(
			err,
			"unable to get pods",
		)
	}

	deployments, err := kube.GetDeployments()
	if err != nil {
		kube.logger.Errorf(
			err,
			"unable to get deployments",
		)
	}

	statefulSets, err := kube.GetStatefulSets()
	if err != nil {
		kube.logger.Errorf(
			err,
			"unable to get statefulSets",
		)
	}

	daemonSets, err := kube.GetDaemonSets()
	if err != nil {
		kube.logger.Errorf(
			err,
			"unable to get daemonSets",
		)
	}

	replicaSets, err := kube.GetReplicaSets()
	if err != nil {
		kube.logger.Errorf(
			err,
			"unable to get replicasets",
		)
	}

	cronJobs, err := kube.GetCronJobs()
	if err != nil {
		kube.logger.Errorf(
			err,
			"unable to get cron jobs",
		)
	}

	limitRangeList, err := kube.GetLimitRanges()
	if err != nil {
		kube.logger.Errorf(
			err,
			"unable to get limitRanges",
		)
	}

	rawResources = map[string]interface{}{
		"pods":         podList,
		"deployments":  deployments,
		"statefulSets": statefulSets,
		"daemonSets":   daemonSets,
		"replicaSets":  replicaSets,
		"cronJobs":     cronJobs,
		"limitRanges":  limitRangeList,
	}

	if podList != nil {
		pods = podList.Items
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

	if limitRangeList != nil {
		limitRanges = limitRangeList.Items
	}

	return
}

func newInt32Pointer(val int32) *int32 {
	res := new(int32)
	*res = val
	return res
}

// GetPods get kubernetes pods
func (kube *Kube) GetPods() (*kv1.PodList, error) {
	kube.logger.Debugf(nil, "{kubernetes} retrieving list of pods")
	podList, err := kube.core.Pods("").List(kmeta.ListOptions{})
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to retrieve pods from all namespaces",
		)
	}

	return podList, nil
}

// GetReplicationControllers get replication controllers
func (kube *Kube) GetReplicationControllers() (
	*kv1.ReplicationControllerList, error,
) {
	kube.logger.Debugf(nil, "{kubernetes} retrieving list of replication controllers")
	controllers, err := kube.core.ReplicationControllers("").
		List(kmeta.ListOptions{})
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to retrieve replication controllers from all namespaces",
		)
	}

	if controllers != nil {
		for _, item := range controllers.Items {
			maskPodSpec(&item.Spec.Template.Spec)
		}
	}

	return controllers, nil
}

// GetDeployments get deployments
func (kube *Kube) GetDeployments() (*kbeta2.DeploymentList, error) {
	kube.logger.Debugf(nil, "{kubernetes} retrieving list of deployments")
	deployments, err := kube.apps.Deployments("").List(kmeta.ListOptions{})
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to retrieve deployments from all namespaces",
		)
	}

	if deployments != nil {
		for _, item := range deployments.Items {
			maskPodSpec(&item.Spec.Template.Spec)
		}
	}

	return deployments, nil
}

// GetStatefulSets get statuful sets
func (kube *Kube) GetStatefulSets() (
	*kbeta2.StatefulSetList, error,
) {
	kube.logger.Debugf(nil, "{kubernetes} retrieving list of stateful sets")
	statefulSets, err := kube.apps.
		StatefulSets("").
		List(kmeta.ListOptions{})
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to retrieve stateful sets from all namespaces",
		)
	}

	if statefulSets != nil {
		for _, item := range statefulSets.Items {
			maskPodSpec(&item.Spec.Template.Spec)
		}
	}

	return statefulSets, nil
}

// GetDaemonSets get daemon sets
func (kube *Kube) GetDaemonSets() (
	*kbeta2.DaemonSetList, error,
) {
	kube.logger.Debugf(nil, "{kubernetes} retrieving list of daemon sets")
	daemonSets, err := kube.apps.
		DaemonSets("").
		List(kmeta.ListOptions{})
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to retrieve daemon sets from all namespaces",
		)
	}

	if daemonSets != nil {
		for _, item := range daemonSets.Items {
			maskPodSpec(&item.Spec.Template.Spec)
		}
	}

	return daemonSets, nil
}

// GetReplicaSets get replicasets
func (kube *Kube) GetReplicaSets() (
	*kbeta2.ReplicaSetList, error,
) {
	kube.logger.Debugf(nil, "{kubernetes} retrieving list of replica sets")
	replicaSets, err := kube.apps.
		ReplicaSets("").
		List(kmeta.ListOptions{})
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to retrieve replica sets from all namespaces",
		)
	}

	if replicaSets != nil {
		for _, item := range replicaSets.Items {
			maskPodSpec(&item.Spec.Template.Spec)
		}
	}

	return replicaSets, nil
}

// GetCronJobs get cron jobs
func (kube *Kube) GetCronJobs() (
	*kbeta1.CronJobList, error,
) {
	kube.logger.Debugf(nil, "{kubernetes} retrieving list of cron jobs")
	cronJobs, err := kube.batch.
		CronJobs("").
		List(kmeta.ListOptions{})
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to retrieve cron jobs from all namespaces",
		)
	}

	if cronJobs != nil {
		for _, item := range cronJobs.Items {
			maskPodSpec(&item.Spec.JobTemplate.Spec.Template.Spec)
		}
	}

	return cronJobs, nil
}

// GetLimitRanges get limits and ranges for namespaces
func (kube *Kube) GetLimitRanges() (
	*kv1.LimitRangeList, error,
) {
	kube.logger.Debugf(nil, "{kubernetes} retrieving list of limitRanges from all namespaces")
	limitRanges, err := kube.core.LimitRanges("").
		List(kmeta.ListOptions{})
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to retrieve list of limitRanges from all namespaces",
		)
	}

	return limitRanges, nil
}

// SetResources set resources for a service
func (kube *Kube) SetResources(kind string, name string, namespace string, totalResources TotalResources) error {
	containerSpecs := []map[string]interface{}{}
	for containerIndex := range totalResources.Containers {
		container := totalResources.Containers[containerIndex]

		var memoryLimits *string
		if container.Limits.Memory != nil {
			tmp := fmt.Sprintf("%dMi", *container.Limits.Memory)
			memoryLimits = &tmp
		}
		var memoryRequests *string
		if container.Requests.Memory != nil {
			tmp := fmt.Sprintf("%dMi", *container.Requests.Memory)
			memoryRequests = &tmp
		}
		var cpuLimits float64
		if container.Limits.CPU != nil {
			cpuLimits = float64(*container.Limits.CPU) / milliCore
		}
		var cpuRequests float64
		if container.Requests.CPU != nil {
			cpuRequests = float64(*container.Requests.CPU) / milliCore
		}
		limits := map[string]interface{}{}
		limits["memory"] = memoryLimits
		limits["cpu"] = cpuLimits
		requests := map[string]interface{}{}
		requests["memory"] = memoryRequests
		requests["cpu"] = cpuRequests

		spec := map[string]interface{}{
			"name": container.Name,
			"resources": map[string]interface{}{
				"limits":   limits,
				"requests": requests,
			},
		}
		containerSpecs = append(containerSpecs, spec)
	}

	body := map[string]interface{}{
		"kind": kind,
		"spec": map[string]interface{}{
			"replicas": totalResources.Replicas,
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": containerSpecs,
				},
			},
		},
	}

	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req := kube.ClientV1Beta2.RESTClient().Patch(types.StrategicMergePatchType).
		Resource(kind + "s").
		Namespace(namespace).
		Name(name).
		Body(bytes.NewBuffer(b))

	res := req.Do()

	_, err = res.Get()
	return err
}

func maskPodSpec(podSpec *kv1.PodSpec) {
	podSpec.Containers = maskContainers(podSpec.Containers)
	podSpec.InitContainers = maskContainers(podSpec.InitContainers)

}

func maskContainers(containers []kv1.Container) (
	masked []kv1.Container,
) {
	for _, container := range containers {
		container.Env = maskEnvVars(container.Env)
		container.Args = maskArgs(container.Args)
		masked = append(masked, container)
	}
	return
}

func maskEnvVars(env [] kv1.EnvVar) (masked [] kv1.EnvVar) {
	masked = make([]kv1.EnvVar, len(env))
	for i, envVar := range env {
		if envVar.Value != "" {
			envVar.Value = maskedValue
		}
		masked[i] = envVar
	}
	return
}

func maskArgs(args []string) (masked []string) {
	masked = make([]string, len(args))
	for i := range args {
		masked[i] = maskedValue
	}
	return
}
