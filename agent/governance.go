package agent

import (
	"context"
	"time"

	"github.com/MagalixTechnologies/uuid-go"
)

type Match struct {
	Namespaces []string
	Kinds      []string
}

type Constraint struct {
	Id         uuid.UUID
	TemplateId uuid.UUID
	AccountId  uuid.UUID
	ClusterId  uuid.UUID

	Name         string
	TemplateName string
	Parameters   map[string]interface{}
	Match        Match
	Code         string

	UpdatedAt time.Time
}

type AuditResult struct {
	TemplateID   uuid.UUID
	ConstraintID uuid.UUID

	HasViolation bool
	Msg          string

	EntityName    *string
	EntityKind    *string
	NamespaceName *string
	ParentName    *string
	ParentKind    *string
	NodeIP        *string
}

type AuditResultHandler func(auditResult []*AuditResult) error

type Auditor interface {
	Start(ctx context.Context) error
	Stop() error

	HandleConstraints(constraint []*Constraint) map[string]error
	SetAuditResultHandler(handler AuditResultHandler)
}
