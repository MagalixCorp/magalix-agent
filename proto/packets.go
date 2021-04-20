package proto

// go:generate make generate

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/MagalixTechnologies/uuid-go"
	"github.com/golang/snappy"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type PacketHello struct {
	Major            uint      `json:"major"`
	Minor            uint      `json:"minor"`
	Build            string    `json:"build"`
	StartID          string    `json:"start_id"`
	AccountID        uuid.UUID `json:"account_id"`
	ClusterID        uuid.UUID `json:"cluster_id"`
	PacketV2Enabled  bool      `json:"packet_v2_enabled,omitempty"`
	ServerVersion    string    `json:"server_version"`
	AgentPermissions string    `json:"agent_permissions"`
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
	Date time.Time `json:"date"`
	Data string    `json:"data"`
}

type ReplicasStatus struct {
	Desired   *int32 `json:"desired,omitempty"`
	Current   *int32 `json:"current,omitempty"`
	Ready     *int32 `json:"ready,omitempty"`
	Available *int32 `json:"available,omitempty"`
}

const (
	ResourceRequirementKindSet                = "set"
	ResourceRequirementKindDefaultsLimitRange = "defaults-limit-range"
	ResourceRequirementKindDefaultFromLimits  = "default-from-limits"
)

type PacketMetricsStoreV2Request []MetricStoreV2Request

type MetricStoreV2Request struct {
	Name           string    `json:"name"`
	Type           string    `json:"type"`
	NodeName       string    `json:"node_name"`
	NamespaceName  string    `json:"namespace_name"`
	ControllerName string    `json:"controller_name"`
	ControllerKind string    `json:"controller_kind"`
	ContainerName  string    `json:"container_name"`
	Timestamp      time.Time `json:"timestamp"`
	Value          int64     `json:"value"`
	PodName        string    `json:"pod_name"`

	AdditionalTags map[string]interface{} `json:"additional_tags"`
}

type PacketLogs []PacketLogItem

type RequestLimit struct {
	CPU    *int64 `json:"cpu,omitempty"`
	Memory *int64 `json:"memory,omitempty"`
}

type ContainerResources struct {
	Requests *RequestLimit `json:"requests,omitempty"`
	Limits   *RequestLimit `json:"limits,omitempty"`
}

type PacketAutomation struct {
	ID string `json:"id"`

	NamespaceName  string `json:"namespace_name"`
	ControllerName string `json:"controller_name"`
	ControllerKind string `json:"controller_kind"`
	ContainerName  string `json:"container_name"`

	ContainerResources ContainerResources `json:"container_resources"`
}

type PacketAutomationFeedbackRequest struct {
	ID string `json:"id"`

	NamespaceName  string `json:"namespace_name"`
	ControllerName string `json:"controller_name"`
	ControllerKind string `json:"controller_kind"`
	ContainerName  string `json:"container_name"`

	Status  AutomationStatus `json:"status"`
	Message string           `json:"message"`
}

type AutomationStatus string

const (
	AutomationExecuted AutomationStatus = "executed"
	AutomationFailed   AutomationStatus = "failed"
	AutomationSkipped  AutomationStatus = "skipped"
)

type PacketAutomationResponse struct {
	ID    string  `json:"id"`
	Error *string `json:"error"`
}

type PacketRestart struct {
	Status int `json:"status"`
}

// PacketLogLevel used to change current log level
type PacketLogLevel struct {
	Level string `json:"level"`
}

type EntityDeltaKind string

const (
	EntityEventTypeUpsert EntityDeltaKind = "UPSERT"
	EntityEventTypeDelete EntityDeltaKind = "DELETE"
)

type ParentController struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	APIVersion string `json:"api_version"`
	IsWatched  bool   `json:"is_watched"`

	Parent *ParentController `json:"parent"`
}

type GroupVersionResourceKind struct {
	schema.GroupVersionResource
	Kind string `json:"kind"`
}

type PacketEntityDelta struct {
	Gvrk      GroupVersionResourceKind  `json:"gvrk"`
	DeltaKind EntityDeltaKind           `json:"delta_kind"`
	Data      unstructured.Unstructured `json:"data"`
	Parent    *ParentController         `json:"parents"`
	Timestamp time.Time                 `json:"timestamp"`
}

type PacketEntitiesDeltasRequest struct {
	Items     []PacketEntityDelta `json:"items"`
	Timestamp time.Time           `json:"timestamp"`
}
type PacketEntitiesDeltasResponse struct{}

type PacketEntitiesResyncItem struct {
	Gvrk GroupVersionResourceKind     `json:"gvrk"`
	Data []*unstructured.Unstructured `json:"data"`
}

type PacketEntitiesResyncRequest struct {
	Timestamp time.Time `json:"timestamp"`

	// map of entities kind and entities definitions
	// it holds other entities not already specified in attributes above
	Snapshot map[string]PacketEntitiesResyncItem `json:"snapshot"`
}
type PacketEntitiesResyncResponse struct{}

type AuditResultStatus string

const (
	AuditResultStatusViolating = "Violation"
	AuditResultStatusCompliant = "Compliance"
	AuditResultStatusIgnored   = "Ignored"
	AuditResultStatusError     = "Error"
)

type PacketAuditResultItem struct {
	TemplateID   *string `json:"template_id"`
	ConstraintID *string `json:"constraint_id"`
	CategoryID   *string `json:"category_id"`
	Severity     *string `json:"severity"`

	Description string `json:"description"`
	HowToSolve  string `json:"how_to_solve"`

	Status AuditResultStatus `json:"status"`
	Msg    *string           `json:"msg"`

	EntityName    *string                `json:"entity_name"`
	EntityKind    *string                `json:"entity_kind"`
	NamespaceName *string                `json:"namespace_name,omitempty"`
	ParentName    *string                `json:"parent_name,omitempty"`
	ParentKind    *string                `json:"parent_kind,omitempty"`
	EntitySpec    map[string]interface{} `json:"entity_spec"`
}

type PacketAuditResultRequest struct {
	Items     []*PacketAuditResultItem `json:"items"`
	Timestamp time.Time                `json:"timestamp"`
}

type Match struct {
	Namespaces []string `json:"namespaces"`
	Kinds      []string `json:"kinds"`
}

type PacketConstraintItem struct {
	Id         string `json:"id"`
	TemplateId string `json:"template_id"`
	AccountId  string `json:"account_id"`
	ClusterId  string `json:"cluster_id"`

	Name         string                 `json:"name"`
	TemplateName string                 `json:"template_name"`
	Parameters   map[string]interface{} `json:"parameters"`
	Match        Match                  `json:"match"`
	Code         string                 `json:"code"`
	Description  string                 `json:"description"`
	HowToSolve   string                 `json:"how_to_solve"`

	CategoryId string    `json:"category_id"`
	Severity   string    `json:"severity"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type PacketConstraintsRequest struct {
	Timestamp   time.Time              `json:"timestamp"`
	Constraints []PacketConstraintItem `json:"constraints"`
}

type PacketConstraintsResponse struct{}

func EncodeSnappy(in interface{}) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			err = fmt.Errorf("%s panic: %v", stack, r)
		}
	}()

	jsonIn, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("unable to encode to snappy, error: %w", err)
	}
	out = snappy.Encode(nil, jsonIn)
	return out, err
}

func DecodeSnappy(in []byte, out interface{}) error {
	jsonIn, err := snappy.Decode(nil, in)
	if err != nil {
		return fmt.Errorf("unable to decode to snappy, error: %w", err)
	}
	return json.Unmarshal(jsonIn, out)
}

func DecodeJSON(in []byte, out interface{}) error {
	return json.Unmarshal(in, out)
}

func EncodeJSON(in interface{}) ([]byte, error) {
	return json.Marshal(in)
}
