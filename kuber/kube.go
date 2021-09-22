package kuber

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"k8s.io/client-go/discovery"

	"github.com/MagalixCorp/magalix-agent/v3/proto"
	"github.com/MagalixTechnologies/core/logger"
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
