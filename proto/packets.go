package proto

// go:generate make generate

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"time"

	"github.com/MagalixCorp/magalix-agent/watcher"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/kovetskiy/lorg"
	satori "github.com/satori/go.uuid"
	"k8s.io/api/apps/v1beta2"
	"k8s.io/api/batch/v1beta1"
	kv1 "k8s.io/api/core/v1"
)

var (
	gobTypesRegistered bool
	gobTypes           = []interface{}{
		uuid.UUID{},
		satori.UUID{},
		[uuid.Size]byte{},

		new(watcher.Status),
		new(watcher.ContainerStatusSource),

		new(kv1.NodeList),
		new(kv1.LimitRangeList),
		new(kv1.PodList),

		new(v1beta1.CronJobList),

		new(v1beta2.DaemonSetList),
		new(v1beta2.StatefulSetList),
		new(v1beta2.ReplicaSetList),
		new(v1beta2.DeploymentList),

		new(map[string]interface{}),
		new(interface{}),
		new([]interface{}),
	}
)

type PacketHello struct {
	Major     uint      `json:"major"`
	Minor     uint      `json:"minor"`
	Build     string    `json:"build"`
	StartID   string    `json:"start_id"`
	AccountID uuid.UUID `json:"account_id"`
	ClusterID uuid.UUID `json:"cluster_id"`
}

type PacketAuthorizationRequest struct {
	AccountID uuid.UUID `json:"account_id"`
	ClusterID uuid.UUID `json:"cluster_id"`
}

type PacketAuthorizationQuestion struct {
	Token []byte `json:"token"`
}

type PacketAuthorizationAnswer struct {
	Token []byte `json:"token"`
}

type PacketAuthorizationFailure struct{}

type PacketAuthorizationSuccess struct{}

type PacketBye struct {
	Reason string `json:"reason,omitempty"`
}

type PacketPing struct {
	Number  int       `json:"number,omitempty"`
	Started time.Time `json:"started"`
}

type PacketPong struct {
	Number  int       `json:"number,omitempty"`
	Started time.Time `json:"started"`
}

type PacketLogItem struct {
	Level lorg.Level  `json:"level"`
	Date  time.Time   `json:"date"`
	Data  interface{} `json:"data"`
}

type PacketRegisterEntityItem struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Kind string    `json:"kind,omitempty"`

	Annotations map[string]string `json:"annotations,omitempty"`
}

type PacketRegisterApplicationItem struct {
	PacketRegisterEntityItem

	LimitRanges []kv1.LimitRange            `json:"limit_ranges"`
	Services    []PacketRegisterServiceItem `json:"services"`
}

type PacketRegisterServiceItem struct {
	PacketRegisterEntityItem
	ReplicasStatus ReplicasStatus                `json:"replicas_status,omitempty"`
	Containers     []PacketRegisterContainerItem `json:"containers"`
}

type ReplicasStatus struct {
	Desired   *int32 `json:"desired,omitempty"`
	Ready     *int32 `json:"ready,omitempty"`
	Available *int32 `json:"available,omitempty"`
}

type PacketRegisterContainerItem struct {
	PacketRegisterEntityItem

	Image     string          `json:"image"`
	Resources json.RawMessage `json:"resources"`
}

type ContainerResourceRequirements struct {
	kv1.ResourceRequirements
	SpecResourceRequirements kv1.ResourceRequirements `json:"spec_resources_requirements,omitempty"`

	LimitsKinds   ResourcesRequirementsKind `json:"limits_kinds,omitempty"`
	RequestsKinds ResourcesRequirementsKind `json:"requests_kinds,omitempty"`
}

type ResourcesRequirementsKind = map[kv1.ResourceName]string

const (
	ResourceRequirementKindSet                = "set"
	ResourceRequirementKindDefaultsLimitRange = "defaults-limit-range"
	ResourceRequirementKindDefaultFromLimits  = "default-from-limits"
)

type PacketApplicationsStoreRequest []PacketRegisterApplicationItem

type PacketApplicationsStoreResponse struct{}

type PacketMetricsStoreRequest []MetricStoreRequest

type MetricStoreRequest struct {
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Node        uuid.UUID `json:"node"`
	Application uuid.UUID `json:"application"`
	Service     uuid.UUID `json:"service"`
	Container   uuid.UUID `json:"container"`
	Timestamp   time.Time `json:"timestamp"`
	Value       int64     `json:"value"`
	Pod         string    `json:"pod"`

	AdditionalTags map[string]interface{} `json:"additional_tags"`
}

type PacketMetricsStoreResponse struct {
}

type PacketMetricValueItem struct {
	Node        *uuid.UUID
	Application *uuid.UUID
	Service     *uuid.UUID
	Container   *uuid.UUID

	Tags  map[string]string
	Value float64
}

type PacketMetricFamilyItem struct {
	Name string
	Help string
	Type string
	Tags []string

	Values []*PacketMetricValueItem
}
type PacketMetricsPromStoreRequest struct {
	Timestamp time.Time

	Metrics []*PacketMetricFamilyItem
}

type PacketMetricsPromStoreResponse struct {
}

type PacketRegisterNodeCapacityItem struct {
	CPU              int `json:"cpu"`
	Memory           int `json:"memory"`
	StorageEphemeral int `json:"storage_ephemeral"`
	Pods             int `json:"pods"`
}

type PacketRegisterNodeItem struct {
	ID            uuid.UUID                              `json:"id,omitempty"`
	Name          string                                 `json:"name"`
	IP            string                                 `json:"ip"`
	Region        string                                 `json:"region,omitempty"`
	Provider      string                                 `json:"provider,omitempty"`
	InstanceType  string                                 `json:"instance_type,omitempty"`
	InstanceSize  string                                 `json:"instance_size,omitempty"`
	Capacity      PacketRegisterNodeCapacityItem         `json:"capacity"`
	Allocatable   PacketRegisterNodeCapacityItem         `json:"allocatable"`
	Containers    int                                    `json:"containers,omitempty"`
	ContainerList []*PacketRegisterNodeContainerListItem `json:"container_list,omitempty"`
}

type PacketRegisterNodeContainerListItem struct {
	// cluster where host of container located in
	Cluster string `json:"cluster"`
	// image of container
	Image string `json:"image"`
	// limits of container
	Limits *PacketRegisterNodeContainerListResourcesItem `json:"limits"`
	// requests of container
	Requests *PacketRegisterNodeContainerListResourcesItem `json:"requests"`
	// name of container (not guaranteed to be unique in cluster scope)
	Name string `json:"name"`
	// namespace where pod located in
	Namespace string `json:"namespace"`
	// node where container located in
	Node string `json:"node"`
	// pod where container located in
	Pod string `json:"pod"`
}

// PacketRegisterNodeContainerListResourcesItem
type PacketRegisterNodeContainerListResourcesItem struct {
	CPU    int `json:"cpu"`
	Memory int `json:"memory"`
}

type PacketNodesStoreRequest []PacketRegisterNodeItem

type PacketNodesStoreResponse struct{}

type PacketLogs []PacketLogItem

type PacketEventsStoreRequest []watcher.Event
type PacketEventsStoreResponse struct{}

type PacketEventLastValueRequest struct {
	Entity    string `json:"entity"`
	EntityID  string `json:"entity_id"`
	EventKind string `json:"kind"`
}

type PacketEventLastValueResponse struct {
	Value interface{} `json:"value"`
}

type PacketStatusStoreRequest struct {
	Entity    string                         `json:"entity"`
	EntityID  uuid.UUID                      `json:"entity_id"`
	Status    watcher.Status                 `json:"status"`
	Source    *watcher.ContainerStatusSource `json:"source"`
	Timestamp time.Time                      `json:"timestamp"`
}

type PacketStatusStoreResponse struct{}

type RequestLimit struct {
	CPU    *int64 `json:"cpu,omitempty"`
	Memory *int64 `json:"memory,omitempty"`
}

type ContainerResources struct {
	ContainerId uuid.UUID    `json:"container_id"`
	Requests    RequestLimit `json:"requests,omitempty"`
	Limits      RequestLimit `json:"limits,omitempty"`
}

type TotalResources struct {
	Replicas   *int                 `json:"replicas,omitempty"`
	Containers []ContainerResources `json:"containers"`
}

type Decision struct {
	ID             uuid.UUID      `json:"id"`
	ServiceId      uuid.UUID      `json:"service_id"`
	TotalResources TotalResources `json:"total_resources"`
}

type PacketDecisions []Decision

type DecisionExecutionStatus string

const (
	DecisionExecutionStatusSucceed DecisionExecutionStatus = "succeed"
	DecisionExecutionStatusFailed  DecisionExecutionStatus = "failed"
	DecisionExecutionStatusSkipped DecisionExecutionStatus = "skipped"
)

type DecisionExecutionResponse struct {
	ID          uuid.UUID               `json:"id"`
	Status      DecisionExecutionStatus `json:"status"`
	Message     string                  `json:"message"`
	ServiceId   uuid.UUID               `json:"service_id"`
	ContainerId *uuid.UUID              `json:"container_id"`
}

type PacketDecisionsResponse []DecisionExecutionResponse

type PacketRestart struct {
	Staus int `json:"status"`
}

type PacketRaw map[string]interface{}
type PacketRawRequest struct {
	PacketRaw

	Timestamp time.Time
}
type PacketRawResponse struct{}

func Decode(in []byte, out interface{}) error {
	return DecodeGOB(in, out)
}

func Encode(in interface{}) ([]byte, error) {
	return EncodeGOB(in)
}

func DecodeGOB(in []byte, out interface{}) error {
	RegisterGOBTypes()
	inBuf := bytes.NewBuffer(in)
	dec := gob.NewDecoder(inBuf)
	return dec.Decode(out)
}

func EncodeGOB(in interface{}) ([]byte, error) {
	RegisterGOBTypes()
	var outBuf bytes.Buffer
	enc := gob.NewEncoder(&outBuf)
	if err := enc.Encode(in); err != nil {
		return nil, err
	}
	return outBuf.Bytes(), nil
}

func DecodeJSON(in []byte, out interface{}) error {
	return json.Unmarshal(in, out)
}

func EncodeJSON(in interface{}) ([]byte, error) {
	return json.Marshal(in)
}

func RegisterGOBTypes() {
	if !gobTypesRegistered {
		for _, t := range gobTypes {
			gob.Register(t)
		}
		gobTypesRegistered = true
	}
}
