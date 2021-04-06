package agent

import (
	"context"
	"time"
)

type Match struct {
	Namespaces []string
	Kinds      []string
}

type Constraint struct {
	Id         string
	TemplateId string
	AccountId  string
	ClusterId  string

	Name         string
	TemplateName string
	Parameters   map[string]interface{}
	Match        Match
	Code         string

	UpdatedAt  time.Time
	CategoryId string
	Severity   string
}

type AuditResultStatus string

const (
	AuditResultStatusViolating = "violation"
	AuditResultStatusCompliant = "compliance"
	AuditResultStatusIgnored   = "ignored"
)

type AuditResult struct {
	TemplateID   *string
	ConstraintID *string
	CategoryID   *string
	Severity     *string

	Status AuditResultStatus
	Msg    *string

	EntityName    *string
	EntityKind    *string
	NamespaceName *string
	ParentName    *string
	ParentKind    *string
	NodeIP        *string
	EntitySpec    map[string]interface{}
}

type AuditResultHandler func(auditResult []*AuditResult) error

type Auditor interface {
	Start(ctx context.Context) error
	Stop() error

	HandleConstraints(constraint []*Constraint) map[string]error
	HandleAuditCommand() error
	SetAuditResultHandler(handler AuditResultHandler)
}
