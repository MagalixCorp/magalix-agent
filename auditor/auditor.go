package auditor

import (
	"context"
	"fmt"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/agent"
	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixTechnologies/core/logger"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	opa "github.com/open-policy-agent/frameworks/constraint/pkg/client"
	opaTypes "github.com/open-policy-agent/frameworks/constraint/pkg/types"
)

const (
	auditInterval           = 24 * time.Hour
	handleConstraintTimeout = 10 * time.Second
	gateKeeperActionDeny    = "deny"

	CrdKindConstraintTemplate        = "ConstraintTemplate"
	CrdAPIVersionGkTemplateV1Beta1   = "templates.gatekeeper.sh/v1beta1"
	CrdAPIVersionGkConstraintV1Beta1 = "constraints.gatekeeper.sh/v1beta1"

	AnnotationKeyTemplateId           = "mgx-template-id"
	AnnotationKeyTemplateName         = "mgx-template-name"
	AnnotationKeyConstraintId         = "mgx-constraint-id"
	AnnotationKeyConstraintName       = "mgx-constraint-name"
	AnnotationKeyConstraintCategoryId = "mgx-constraint-category-id"
	AnnotationKeyConstraintSeverity   = "mgx-constraint-category-severity"
)

type Auditor struct {
	opa                 *opa.Client
	parentsStore        *kuber.ParentsStore
	constraintsRegistry ConstraintRegistry
	sendAuditResult     agent.AuditResultHandler

	updated               chan struct{}
	ctx                   context.Context
	cancelWorker          context.CancelFunc
	waitEntitiesCacheSync chan struct{}
}

func NewAuditor(opaClient *opa.Client, parentsStore *kuber.ParentsStore, waitEntitiesCacheSync chan struct{}) *Auditor {
	return &Auditor{
		opa:                   opaClient,
		parentsStore:          parentsStore,
		constraintsRegistry:   NewConstraintRegistry(),
		updated:               make(chan struct{}),
		waitEntitiesCacheSync: waitEntitiesCacheSync,
	}
}

func (a *Auditor) SetAuditResultHandler(handler agent.AuditResultHandler) {
	a.sendAuditResult = handler
}

func (a *Auditor) HandleConstraints(constraints []*agent.Constraint) map[string]error {

	errorsMap := make(map[string]error, 0)
	changed := false
	for _, constraint := range constraints {
		shouldUpdate, err := a.constraintsRegistry.ShouldUpdate(constraint)
		if err != nil {
			logger.Errorf("couldn't check constraint. %w", err)
			continue
		}

		if shouldUpdate {
			err := a.addConstraint(constraint)
			if err != nil {
				errorsMap[constraint.Id.String()] = fmt.Errorf("couldn't add opa template. %w", err)
			}

			changed = true

			err = a.constraintsRegistry.RegisterConstraint(constraint)
			if err != nil {
				logger.Errorf("couldn't register constraint. %w", err)
			}
		}
	}

	for _, info := range a.constraintsRegistry.FindConstraintsToDelete(constraints) {
		err := a.deleteConstraint(info)
		if err != nil {
			logger.Errorf("couldn't delete constraint %s. %w", info, err)
		}
		changed = true
	}

	if changed {
		a.updated <- struct{}{}
	}

	return errorsMap
}

func (a *Auditor) addConstraint(constraint *agent.Constraint) error {
	timedCtx, cancel := context.WithTimeout(a.ctx, handleConstraintTimeout)
	defer cancel()

	opaTemplate, opaConstraint, err := convertMgxConstraintToOpaTemplateAndConstraint(constraint)
	if err != nil {
		return fmt.Errorf("couldn't convert mgx constraint to opa template and constraint. %w", err)
	}
	_, err = a.opa.AddTemplate(timedCtx, opaTemplate)
	if err != nil {
		return fmt.Errorf("couldn't add opa template. %w", err)
	}
	_, err = a.opa.AddConstraint(timedCtx, opaConstraint)
	if err != nil {
		return fmt.Errorf("couldn't add opa constraint. %w", err)
	}

	return nil
}

func (a *Auditor) deleteConstraint(info *ConstrainInfo) error {
	timedCtx, cancel := context.WithTimeout(a.ctx, handleConstraintTimeout)
	defer cancel()

	constraint := info.ToOpaConstraint()
	template := info.ToOpaTemplate()
	_, err := a.opa.RemoveTemplate(timedCtx, template)
	if err != nil {
		return fmt.Errorf("couldn't remove template. %w", err)
	}
	_, err = a.opa.RemoveConstraint(timedCtx, constraint)
	if err != nil {
		return fmt.Errorf("couldn't remove constraint. %w", err)
	}

	err = a.constraintsRegistry.UnregisterConstraint(info)
	if err != nil {
		return fmt.Errorf("couldn't unregister constraint. %w", err)
	}

	return nil
}

func (a *Auditor) audit(ctx context.Context) ([]*opaTypes.Result, error) {

	resp, err := a.opa.Audit(ctx)
	if err != nil {
		logger.Errorw("Error while performing audit", "error", err)
		return nil, err
	}
	results := resp.Results()

	return results, nil
}

func (a *Auditor) Start(ctx context.Context) error {
	logger.Info(" Audit worker started")
	if a.cancelWorker != nil {
		a.cancelWorker()
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	a.ctx = cancelCtx
	a.cancelWorker = cancel

	auditTicker := time.NewTicker(auditInterval)
	for {
		shouldAudit := false
		select {
		case <-cancelCtx.Done():
			return nil
		case <-a.updated:
			shouldAudit = true
		case <-a.waitEntitiesCacheSync:
			shouldAudit = true
		case <-auditTicker.C:
			shouldAudit = true
		}

		if shouldAudit {
			shouldAudit = false

			results, err := a.audit(cancelCtx)
			if err != nil {
				return err
			}

			err = a.convertAndSendAuditResults(results)
			if err != nil {
				logger.Errorw("Error while sending audit results", "error", err)
			}
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

func (a *Auditor) convertAndSendAuditResults(results []*opaTypes.Result) error {
	mgxRes, err := a.convertGkAuditResultToMgxAuditResult(results)
	if err != nil {
		return fmt.Errorf("error while converting opa results to magalix audit results. %w", err)
	}
	err = a.sendAuditResult(mgxRes)
	if err != nil {
		return fmt.Errorf("error while sending audit results. %w", err)
	}
	return nil
}

func (a *Auditor) convertGkAuditResultToMgxAuditResult(in []*opaTypes.Result) ([]*agent.AuditResult, error) {
	out := make([]*agent.AuditResult, 0, len(in))
	for _, gkRes := range in {
		resource, ok := gkRes.Resource.(*unstructured.Unstructured)
		if !ok {
			err := fmt.Errorf("couldn't cast result to unstructred")
			logger.Errorw("Couldn't get resource from audit result", "error", err)
			return nil, err
		}

		templateId, err := getUuidFromAnnotation(gkRes.Constraint, AnnotationKeyTemplateId)
		if err != nil {
			return nil, fmt.Errorf("couldn't get template id from constraint annotations. %w", err)
		}
		constraintId, err := getUuidFromAnnotation(gkRes.Constraint, AnnotationKeyConstraintId)
		if err != nil {
			return nil, fmt.Errorf("couldn't get constraint id from constraint annotations. %w", err)
		}
		categoryId, err := getUuidFromAnnotation(gkRes.Constraint, AnnotationKeyConstraintCategoryId)
		if err != nil {
			return nil, fmt.Errorf("couldn't get constraint category id from constraint annotations. %w", err)
		}
		severity, err := getStringFromAnnotation(gkRes.Constraint, AnnotationKeyConstraintSeverity)
		if err != nil {
			return nil, fmt.Errorf("couldn't get constraint severity from constraint annotations. %w", err)
		}

		hasViolation := gkRes.EnforcementAction == gateKeeperActionDeny
		msg := gkRes.Msg

		// Get resource identity info based on entity type
		namespace := resource.GetNamespace()
		kind := resource.GetKind()
		name := resource.GetName()
		parent, found := a.parentsStore.GetParents(namespace, kind, name)
		var parentName, parentKind string
		if found && parent != nil {
			// RootParent func should move outside kuber
			topParent := kuber.RootParent(parent)
			parentName = topParent.Name
			parentKind = topParent.Kind
		}

		var nodeIp string
		if kind == kuber.Nodes.Kind {
			ip, err := getNodeIpFromUnstructured(resource)
			if err != nil {
				logger.Errorf("couldn't get node ip. %w", err)
			} else {
				nodeIp = ip
			}
		}

		mgxRes := agent.AuditResult{
			TemplateID:    templateId,
			ConstraintID:  constraintId,
			HasViolation:  hasViolation,
			Msg:           msg,
			EntityName:    &name,
			EntityKind:    &kind,
			ParentName:    &parentName,
			ParentKind:    &parentKind,
			NamespaceName: &namespace,
			NodeIP:        &nodeIp,
			CategoryID:    categoryId,
			Severity:      severity,
		}
		out = append(out, &mgxRes)
	}

	return out, nil
}
