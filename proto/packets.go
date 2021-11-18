package proto

// go:generate make generate

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/MagalixTechnologies/uuid-go"
	"github.com/golang/snappy"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ResourceRequirementKindSet                                = "set"
	ResourceRequirementKindDefaultsLimitRange                 = "defaults-limit-range"
	ResourceRequirementKindDefaultFromLimits                  = "default-from-limits"
	EntityEventTypeUpsert                     EntityDeltaKind = "UPSERT"
	EntityEventTypeDelete                     EntityDeltaKind = "DELETE"
	AuditResultStatusViolating                                = "Violation"
	AuditResultStatusCompliant                                = "Compliance"
	AuditResultStatusIgnored                                  = "Ignored"
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
	ClusterProvider  string    `json:"cluster_provider"`
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

type PacketLogs []PacketLogItem

type RequestLimit struct {
	CPU    *int64 `json:"cpu,omitempty"`
	Memory *int64 `json:"memory,omitempty"`
}

type PacketRestart struct {
	Status int `json:"status"`
}

// PacketLogLevel used to change current log level
type PacketLogLevel struct {
	Level string `json:"level"`
}

type EntityDeltaKind string

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

type AuditResultStatus string

type PacketAuditResultItem struct {
	Id           string   `json:"id"`
	TemplateID   *string  `json:"template_id"`
	ConstraintID *string  `json:"constraint_id"`
	CategoryID   *string  `json:"category_id"`
	Severity     *string  `json:"severity"`
	Controls     []string `json:"controls"`
	Standards    []string `json:"standards"`

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
	Trigger       string                 `json:"trigger"`
}

type PacketAuditResultRequest struct {
	Items     []*PacketAuditResultItem `json:"items"`
	Timestamp time.Time                `json:"timestamp"`
}

type Match struct {
	Namespaces []string            `json:"namespaces"`
	Kinds      []string            `json:"kinds"`
	Labels     []map[string]string `json:"labels"`
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
	Controls   []string  `json:"controls"`
	Standards  []string  `json:"standards"`
	UpdatedAt  time.Time `json:"updated_at"`
	DeletedAt  *string   `json:"deleted_at,omitempty"`
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
