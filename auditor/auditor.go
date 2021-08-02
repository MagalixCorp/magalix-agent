package auditor

import (
	"context"
	"time"

	"github.com/pkg/errors"

	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"github.com/MagalixCorp/magalix-agent/v3/entities"
	"github.com/MagalixCorp/magalix-agent/v3/kuber"
	"github.com/MagalixTechnologies/core/logger"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	opa "github.com/MagalixCorp/magalix-agent/v3/auditor/opa-auditor"
)

const (
	auditInterval = 23 * time.Hour
)

type AuditEventType string

const (
	AuditEventTypeCommand      AuditEventType = "command"
	AuditEventTypePolicyChange AuditEventType = "policy-change"
	AuditEventTypeEntityChange AuditEventType = "entity-change"
	AuditEventTypeEntityDelete AuditEventType = "entity-delete"
	AuditEventTypeEntitiesSync AuditEventType = "entities-sync"
	AuditEventTypePeriodic     AuditEventType = "periodic-audit"
	AuditEventTypeInitial      AuditEventType = "initial-audit"
)

type AuditEvent struct {
	Type AuditEventType
	Data interface{}
}

type Auditor struct {
	opa             *opa.OpaAuditor
	entitiesWatcher entities.EntitiesWatcherSource
	sendAuditResult agent.AuditResultHandler

	auditEvents  chan AuditEvent
	ctx          context.Context
	cancelWorker context.CancelFunc
}

func NewAuditor(entitiesWatcher entities.EntitiesWatcherSource) *Auditor {
	a := &Auditor{
		opa:             opa.New(entitiesWatcher),
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
	var event AuditEvent
	if a.opa.GetConstraintsSize() == 0 {
		event = AuditEvent{
			Type: AuditEventTypeInitial,
		}
	} else {
		event = AuditEvent{
			Type: AuditEventTypePolicyChange,
		}
	}

	updatedConstraintIds, errs := a.opa.UpdateConstraints(constraints)
	if len(errs) > 0 {
		logger.Warnw("failed to parse some constraints", "constraints-size", len(errs))
	}
	if len(updatedConstraintIds) > 0 {
		logger.Infof("firing audit event of type %s", event.Type)
		event.Data = updatedConstraintIds
		a.auditEvents <- event
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
		Type: AuditEventTypeEntityChange,
		Data: &obj,
	}
}

func (a *Auditor) OnResourceUpdate(gvrk kuber.GroupVersionResourceKind, oldObj, newObj unstructured.Unstructured) {
	a.auditEvents <- AuditEvent{
		Type: AuditEventTypeEntityChange,
		Data: &newObj,
	}
}

func (a *Auditor) OnResourceDelete(gvrk kuber.GroupVersionResourceKind, obj unstructured.Unstructured) {
	a.auditEvents <- AuditEvent{
		Type: AuditEventTypeEntityDelete,
		Data: &obj,
	}
}

func (a *Auditor) OnCacheSync() {
	a.auditEvents <- AuditEvent{Type: AuditEventTypeEntitiesSync}
}

func (a *Auditor) auditResource(resource *unstructured.Unstructured, constraintIds []string, triggerType string) ([]*agent.AuditResult, error) {
	var err error
	results, errs := a.opa.Audit(resource, constraintIds, triggerType)
	if len(errs) > 0 {
		logger.Errorw("errors while auditing resource", "errors-count", len(errs), "errors", errs)
		err = errors.Wrap(errs[0], "errors while auditing resource")
	}
	return results, err

}

func (a *Auditor) auditAllResourcesAndSendData(constraintIds []string, triggerType string) {
	resourcesByGvrk, errs := a.entitiesWatcher.GetAllEntitiesByGvrk()
	if len(errs) > 0 {
		logger.Errorw("error while getting all resources", "error", errs)
	}
	for _, resources := range resourcesByGvrk {
		for idx := range resources {
			resource := resources[idx]
			results, _ := a.auditResource(&resource, constraintIds, triggerType)
			a.opa.UpdateCache(results)
			err := a.sendAuditResult(results)
			if err != nil {
				logger.Errorw("error while sending audit result", "error", err)
			}
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
			case AuditEventTypeEntityChange:
				if entitiesSynced {
					logger.Debugf("Received update resource audit event. Auditing resource")
					resource := e.Data.(*unstructured.Unstructured)
					results, _ := a.auditResource(resource, nil, string(e.Type))
					nResult := make([]*agent.AuditResult, 0, len(results))
					for i := range results {
						result := results[i]
						if a.opa.CheckResourceStatusWithConstraint(*result.ConstraintID, resource, result.Status) {
							nResult = append(nResult, result)
						}

					}
					results = nResult
					a.opa.UpdateCache(results)
					err := a.sendAuditResult(results)
					if err != nil {
						logger.Errorw("error while sending audit result", "error", err)
					}

				} else {
					logger.Debug("Received update resource audit event. Ignoring as entities are not synced yet")
				}
			case AuditEventTypeEntityDelete:
				logger.Debugf("Received delete resource audit event")
				a.opa.RemoveResource(e.Data.(*unstructured.Unstructured))
			case AuditEventTypePolicyChange, AuditEventTypeInitial:
				updated := e.Data.([]string)
				a.auditAllResourcesAndSendData(updated, string(e.Type))
			case AuditEventTypeEntitiesSync:
				entitiesSynced = true
				fallthrough
			case AuditEventTypeCommand:
				logger.Debug("Received audit command event. Auditing all resources")
				a.auditAllResourcesAndSendData(nil, string(e.Type))
			default:
				logger.Errorw("unsupported event type", "event-type", e.Type)
			}
		case <-auditTicker.C:
			logger.Debug("Starting periodical auditing. Auditing all resources")
			a.auditAllResourcesAndSendData(nil, string(AuditEventTypePeriodic))
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
