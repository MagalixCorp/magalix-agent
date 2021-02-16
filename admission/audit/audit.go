package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/admission/target"

	"github.com/MagalixCorp/magalix-agent/v2/agent"
	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/MagalixTechnologies/uuid-go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	opa "github.com/open-policy-agent/frameworks/constraint/pkg/client"
	opaTemplates "github.com/open-policy-agent/frameworks/constraint/pkg/core/templates"
	opaTypes "github.com/open-policy-agent/frameworks/constraint/pkg/types"
	k8sV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

const (
	// TODO DO NOT ACCEPT IF NOT 24 HOURS!
	auditInterval        = 10 * time.Second
	gateKeeperActionDeny = "deny"

	CrdKindConstraintTemplate        = "ConstraintTemplate"
	CrdAPIVersionGkTemplateV1Beta1   = "templates.gatekeeper.sh/v1beta1"
	CrdAPIVersionGkConstraintV1Beta1 = "constraints.gatekeeper.sh/v1beta1"

	AnnotationKeyTemplateId   = "mgx-template-id"
	AnnotationKeyConstraintId = "mgx-constraint-id"
)

type Auditor struct {
	opa             *opa.Client
	parentsStore    *kuber.ParentsStore
	sendAuditResult agent.AuditResultHandler

	ctx          context.Context
	cancelWorker context.CancelFunc
}

func NewAuditor(opaClient *opa.Client, parentsStore *kuber.ParentsStore) *Auditor {
	return &Auditor{
		opa:          opaClient,
		parentsStore: parentsStore,
	}
}

func (a *Auditor) SetAuditResultHandler(handler agent.AuditResultHandler) {
	a.sendAuditResult = handler
}

func (a *Auditor) AddConstraint(constraint *agent.Constraint) error {
	// TODO: Handle delete

	logger.Info("===RECEIVED CONSTRAINT", constraint)

	opaTemplate, opaConstraint, err := convertMgxConstraintToOpaTemplateAndConstraint(constraint)
	if err != nil {
		return fmt.Errorf("couldn't convert mgx constraint to opa template and constraint. %w", err)
	}
	_, err = a.opa.AddTemplate(a.ctx, opaTemplate)
	if err != nil {
		return fmt.Errorf("couldn't add opa template. %w", err)
	}
	logger.Info("=====Entering Constraint")
	_, err = a.opa.AddConstraint(a.ctx, opaConstraint)
	if err != nil {
		return fmt.Errorf("couldn't add opa constraint. %w", err)
	}
	logger.Info("=====Exiting Constraint")
	return nil
}

func (a *Auditor) audit(ctx context.Context) ([]*opaTypes.Result, error) {
	logger.Info("===Auditing")

	resp, err := a.opa.Audit(ctx)
	if err != nil {
		logger.Errorw("Error while performing audit", "error", err)
		return nil, err
	}
	results := resp.Results()

	logger.Info("===Audit complete ", results)

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
		// As tickers has no way to start immediately, the logic is placed outside
		// the select so it executes immediately on entering the loop before waiting
		// for the first tick from the ticker

		results, err := a.audit(cancelCtx)
		if err != nil {
			return err
		}

		err = a.convertAndSendAuditResults(results)
		if err != nil {
			logger.Errorw("Error while sending audit results", "error", err)
		}

		select {
		case <-cancelCtx.Done():
			return nil
		case <-auditTicker.C:
			continue
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

		var NodeIp string
		if kind == kuber.Nodes.Kind {
			// TODO: Get node IP from unstructured
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
			NodeIP:        &NodeIp,
		}
		out = append(out, &mgxRes)
	}

	return out, nil
}

func getUuidFromAnnotation(obj *unstructured.Unstructured, key string) (uuid.UUID, error) {
	val, ok := obj.GetAnnotations()[key]
	if !ok {
		return uuid.UUID{}, fmt.Errorf("couldn't find %s in annotations", key)
	}
	id, err := uuid.FromString(val)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("id %s isn't a valid UUID. %w", val, err)
	}

	return id, nil
}

const letterBytes = "abcdefghijklmnopqrstuvwxyz"

func generateName() string {
	b := make([]byte, 10)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return string(b)
}

func convertMgxConstraintToOpaTemplateAndConstraint(constraint *agent.Constraint) (
	*opaTemplates.ConstraintTemplate,
	*unstructured.Unstructured,
	error,
) {
	name := generateName()
	opaTemplate := opaTemplates.ConstraintTemplate{
		TypeMeta: k8sV1.TypeMeta{
			Kind:       CrdKindConstraintTemplate,
			APIVersion: CrdAPIVersionGkTemplateV1Beta1,
		},
		ObjectMeta: k8sV1.ObjectMeta{
			Name: name,
			UID:  k8sTypes.UID(constraint.TemplateId.String()),
			Annotations: map[string]string{
				AnnotationKeyTemplateId: constraint.TemplateId.String(),
			},
		},
		Spec: opaTemplates.ConstraintTemplateSpec{
			CRD: opaTemplates.CRD{
				Spec: opaTemplates.CRDSpec{
					Names: opaTemplates.Names{
						Kind: name,
					},
				},
			},
			Targets: []opaTemplates.Target{
				{
					Target: target.TargetName,
					Rego:   constraint.Code,
				},
			},
		},
	}

	kindsMatcher, err := convertKindsListToKindsMatcher(constraint.Match.Kinds)
	if err != nil {
		return nil, nil, fmt.Errorf("couldn't build kind matcher. %w", err)
	}

	namespacesMatcher, err := convertNamespacesListToNAmespacesMatcher(constraint.Match.Namespaces)
	if err != nil {
		return nil, nil, fmt.Errorf("couldn't build namespaces matcher. %w", err)
	}
	logger.Info("%%$$%%%((***&&^%$$$$####", namespacesMatcher, kindsMatcher)

	opaConstraint := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": CrdAPIVersionGkConstraintV1Beta1,
			"kind":       name,
			"metadata": map[string]interface{}{
				"name": name,
				"uid":  constraint.Id.String(),
				"annotations": map[string]interface{}{
					AnnotationKeyTemplateId:   constraint.TemplateId.String(),
					AnnotationKeyConstraintId: constraint.Id.String(),
				},
			},
			"spec": map[string]interface{}{
				"match": map[string]interface{}{
					"kinds":      kindsMatcher,
					"namespaces": namespacesMatcher,
				},
				"parameters": constraint.Parameters,
			},
		},
	}

	tempJson, err := json.Marshal(opaTemplate)
	logger.Info("===OPA TEMPLATE: \n", string(tempJson), err)
	constJson, err := json.Marshal(opaConstraint)
	logger.Info("===OPA CONSTRAINT: \n", string(constJson), err)

	return &opaTemplate, &opaConstraint, nil
}

func convertKindsListToKindsMatcher(kinds []string) ([]interface{}, error) {
	//apiGroups := make([]string, 0)
	apiGroups := []interface{}{"*"}
	matchedKinds := make([]interface{}, 0)

	if len(kinds) == 0 {
		matchedKinds = []interface{}{"*"}
	} else {
		for _, k := range kinds {
			gvrk, err := kuber.KindToGvrk(k)
			if err != nil {
				return nil, fmt.Errorf("unsupported kind %s", k)
			}

			// TODO: Handle repeated kinds and api groups
			//apiGroups = append(apiGroups, gvrk.Group)
			matchedKinds = append(matchedKinds, gvrk.Kind)
		}
	}

	return []interface{}{map[string]interface{}{
		"apiGroups": apiGroups,
		"kinds":     matchedKinds,
	}}, nil
}

func convertNamespacesListToNAmespacesMatcher(namespaces []string) ([]interface{}, error) {
	if len(namespaces) == 0 {
		return []interface{}{"*"}, nil
	}
	result := make([]interface{}, len(namespaces))
	for i, namespace := range namespaces {
		result[i] = namespace
	}
	return result, nil
}
