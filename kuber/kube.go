package kuber

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/MagalixCorp/magalix-agent/client"
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
)

const (
	milliCore = 1000
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
func (kube *Kube) GetNodes() ([]kv1.Node, error) {
	kube.logger.Debugf(nil, "{kubernetes} retrieving list of nodes")
	nodes, err := kube.core.Nodes().List(kmeta.ListOptions{})
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to retrieve nodes from all namespaces",
		)
	}

	return nodes.Items, nil
}

// GetPods get kubernetes pods
func (kube *Kube) GetPods() ([]kv1.Pod, error) {
	kube.logger.Debugf(nil, "{kubernetes} retrieving list of pods")
	pods, err := kube.core.Pods("").List(kmeta.ListOptions{})
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to retrieve pods from all namespaces",
		)
	}

	return pods.Items, nil
}

// GetReplicationControllers get replication controllers
func (kube *Kube) GetReplicationControllers() (
	[]kv1.ReplicationController, error,
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

	return controllers.Items, nil
}

// GetDeployments get deployments
func (kube *Kube) GetDeployments() ([]kbeta2.Deployment, error) {
	kube.logger.Debugf(nil, "{kubernetes} retrieving list of deployments")
	deployments, err := kube.apps.Deployments("").List(kmeta.ListOptions{})
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to retrieve deployments from all namespaces",
		)
	}

	return deployments.Items, nil
}

// GetStatefulSets get statuful sets
func (kube *Kube) GetStatefulSets() (
	[]kbeta2.StatefulSet, error,
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

	return statefulSets.Items, nil
}

// GetDaemonSets get daemon sets
func (kube *Kube) GetDaemonSets() (
	[]kbeta2.DaemonSet, error,
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

	return daemonSets.Items, nil
}

// GetReplicaSets get replicasets
func (kube *Kube) GetReplicaSets() (
	[]kbeta2.ReplicaSet, error,
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

	return replicaSets.Items, nil
}

// GetCronJobs get cron jobs
func (kube *Kube) GetCronJobs() (
	[]kbeta1.CronJob, error,
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

	return cronJobs.Items, nil
}

// GetLimitRanges get limits and ranges for namespaces
func (kube *Kube) GetLimitRanges() (
	[]kv1.LimitRange, error,
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

	return limitRanges.Items, nil
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
