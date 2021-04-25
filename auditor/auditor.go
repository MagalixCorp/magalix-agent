package auditor

import (
	"context"
	"time"

	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"github.com/MagalixCorp/magalix-agent/v3/entities"
	"github.com/MagalixCorp/magalix-agent/v3/kuber"
	"github.com/MagalixTechnologies/core/logger"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	opa "github.com/MagalixCorp/magalix-agent/v3/auditor/opa-auditor"
)

const (
	auditInterval = 24 * time.Hour
)

type AuditEventType string

const (
	AuditEventTypeCommand        AuditEventType = "command"
	AuditEventTypePoliciesChange AuditEventType = "policies-change"
	AuditEventTypeResourceUpdate AuditEventType = "resource-update"
	AuditEventTypeResourceDelete AuditEventType = "resource-delete"
	AuditEventTypeResourcesSync  AuditEventType = "resources-sync"
)

type AuditEvent struct {
	Type AuditEventType
	Data interface{}
}

type Auditor struct {
	opa             *opa.OpaAuditor
	entitiesWatcher *entities.EntitiesWatcher
	sendAuditResult agent.AuditResultHandler

	auditEvents  chan AuditEvent
	ctx          context.Context
	cancelWorker context.CancelFunc
}

func NewAuditor(parentsStore *kuber.ParentsStore, entitiesWatcher *entities.EntitiesWatcher) *Auditor {
	a := &Auditor{
		opa:             opa.New(parentsStore),
		auditEvents:     make(chan AuditEvent),
		entitiesWatcher: entitiesWatcher,
	}
	a.entitiesWatcher.AddResourceEventsHandler(a)
	return a
}

func (a *Auditor) SetAuditResultHandler(handler agent.AuditResultHandler) {
	a.sendAuditResult = handler
}

func (a *Auditor) HandleConstraints(constraints []*agent.Constraint) map[string]error {
	updated, errs := a.opa.UpdateConstraints(constraints)

	if len(updated) > 0 {
		logger.Info("Detected policies change. firing audit event")
		a.auditEvents <- AuditEvent{
			Type: AuditEventTypePoliciesChange,
			Data: updated,
		}
	}

	return errs
}

func (a *Auditor) HandleAuditCommand() error {
	logger.Info("Received audit command. firing audit event")

	go a.triggerAuditCommand()

	return nil
}

func (a *Auditor) triggerAuditCommand() {
	a.auditEvents <- AuditEvent{Type: AuditEventTypeCommand}
}

func (a *Auditor) OnResourceAdd(gvrk kuber.GroupVersionResourceKind, obj unstructured.Unstructured) {
	a.auditEvents <- AuditEvent{
		Type: AuditEventTypeResourceUpdate,
		Data: &obj,
	}
}

func (a *Auditor) OnResourceUpdate(gvrk kuber.GroupVersionResourceKind, oldObj, newObj unstructured.Unstructured) {
	a.auditEvents <- AuditEvent{
		Type: AuditEventTypeResourceUpdate,
		Data: &newObj,
	}
}

func (a *Auditor) OnResourceDelete(gvrk kuber.GroupVersionResourceKind, obj unstructured.Unstructured) {
	a.auditEvents <- AuditEvent{
		Type: AuditEventTypeResourceDelete,
		Data: &obj,
	}
}

func (a *Auditor) OnCacheSync() {
	a.auditEvents <- AuditEvent{Type: AuditEventTypeResourcesSync}
}

func (a *Auditor) auditResource(resource *unstructured.Unstructured, constraintIds []string, useCache bool) {
	results, errs := a.opa.Audit(resource, constraintIds, useCache)
	if len(errs) > 0 {
		logger.Errorw("errors while auditing resource", "errors-count", len(errs), "errors", errs)
	}

	if len(results) > 0 {
		err := a.sendAuditResult(results)
		if err != nil {
			logger.Errorw("error while sending audit result", "error", err)
		}
	}
}

func (a *Auditor) auditAllResources(constraintIds []string, useCache bool) {
	resourcesByGvrk, errs := a.entitiesWatcher.GetAllEntitiesByGvrk()
	if len(errs) > 0 {
		logger.Errorw("error while getting all resources", "error", errs)
	}
	for _, resources := range resourcesByGvrk {
		for _, r := range resources {
			a.auditResource(&r, constraintIds, useCache)
		}
	}
}

func (a *Auditor) Start(ctx context.Context) error {
	logger.Info(" Audit worker started")
	if a.cancelWorker != nil {
		a.cancelWorker()
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	a.ctx = cancelCtx
	a.cancelWorker = cancel

	entitiesSynced := false

	auditTicker := time.NewTicker(auditInterval)
	for {
		select {
		case <-cancelCtx.Done():
			return nil
		case e := <-a.auditEvents:
			switch e.Type {
			case AuditEventTypeResourceUpdate:
				if entitiesSynced {
					logger.Debugf("Received update resource audit event. Auditing resource")
					a.auditResource(e.Data.(*unstructured.Unstructured), nil, true)
				} else {
					logger.Debug("Received update resource audit event. Ignoring as entities are not synced yet")
				}
			case AuditEventTypeResourceDelete:
				logger.Debugf("Received delete resource audit event")
				a.opa.RemoveResource(e.Data.(*unstructured.Unstructured))
			case AuditEventTypePoliciesChange:
				updated := e.Data.([]string)
				a.auditAllResources(updated, false)
			case AuditEventTypeResourcesSync:
				entitiesSynced = true
				fallthrough
			case AuditEventTypeCommand:
				logger.Debug("Received audit command event. Auditing all resources")
				a.auditAllResources(nil, false)
			default:
				logger.Errorw("unsupported event type", "event-type", e.Type)
			}
		case <-auditTicker.C:
			logger.Debug("Starting peridoical auditing. Auditing all resources")
			a.auditAllResources(nil, false)
		}
	}
}

func (a *Auditor) Stop() error {
	if a.cancelWorker == nil {
		return nil
	}

	a.cancelWorker()
	return nil
}
