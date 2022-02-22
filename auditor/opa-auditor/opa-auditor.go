package opa_auditor

import (
	"fmt"
	"sync"
	"time"

	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"github.com/MagalixCorp/magalix-agent/v3/entities"
	"github.com/MagalixCorp/magalix-agent/v3/kuber"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	opa "github.com/MagalixTechnologies/opa-core"
)

const (
	PolicyQuery = "violation"
)

var lock sync.Mutex

type Template struct {
	Id          string
	Name        string
	Policy      opa.Policy
	Description string
	HowToSolve  string
	UsageCount  int
}

type Constraint struct {
	Id         string
	TemplateId string

	Name       string
	Parameters map[string]interface{}
	Match      agent.Match

	UpdatedAt  time.Time
	CategoryId string
	Severity   string
	Controls   []string
	Standards  []string
}

type OpaAuditor struct {
	templates   map[string]*Template
	constraints map[string]*Constraint
	cache       *AuditResultsCache

	entitiesWatcher entities.EntitiesWatcherSource
}

func New(entitiesWatcher entities.EntitiesWatcherSource) *OpaAuditor {
	return &OpaAuditor{
		templates:   make(map[string]*Template),
		constraints: make(map[string]*Constraint),
		cache:       NewAuditResultsCache(),

		entitiesWatcher: entitiesWatcher,
	}
}
func (a *OpaAuditor) GetConstraintsSize() int {
	return len(a.constraints)
}
func (a *OpaAuditor) UpdateConstraint(constraint *agent.Constraint) (bool, error) {

	cId := constraint.Id
	tId := constraint.TemplateId
	c, cFound := a.constraints[cId]
	t, tFound := a.templates[tId]
	updated := false
	if !cFound {
		a.constraints[cId] = &Constraint{
			Id:         cId,
			TemplateId: tId,
			Name:       constraint.Name,
			Parameters: constraint.Parameters,
			Match:      constraint.Match,
			UpdatedAt:  constraint.UpdatedAt,
			CategoryId: constraint.CategoryId,
			Severity:   constraint.Severity,
			Standards:  constraint.Standards,
			Controls:   constraint.Controls,
		}

		if !tFound {
			policy, err := opa.Parse(constraint.Code, PolicyQuery)
			if err != nil {
				return false, errors.Wrapf(
					err, "couldn't parse template %s, template id: %s for constraint: %s, constraint id: %s",
					constraint.TemplateName, constraint.TemplateId, constraint.Name, constraint.Id,
				)
			}

			a.templates[tId] = &Template{
				Id:          tId,
				Name:        constraint.TemplateName,
				Policy:      policy,
				Description: constraint.Description,
				HowToSolve:  constraint.HowToSolve,
				UsageCount:  1,
			}
		} else {
			t.UsageCount++
		}

		updated = true
	} else if cFound && constraint.UpdatedAt.After(c.UpdatedAt) {
		a.constraints[cId] = &Constraint{
			Id:         cId,
			TemplateId: tId,
			Name:       constraint.Name,
			Parameters: constraint.Parameters,
			Match:      constraint.Match,
			UpdatedAt:  constraint.UpdatedAt,
			CategoryId: constraint.CategoryId,
			Severity:   constraint.Severity,
			Standards:  constraint.Standards,
			Controls:   constraint.Controls,
		}

		policy, err := opa.Parse(constraint.Code, PolicyQuery)
		if err != nil {
			return false, errors.Wrapf(
				err, "couldn't parse template %s, template id: %s for constraint: %s, constraint id: %s",
				constraint.TemplateName, constraint.TemplateId, constraint.Name, constraint.Id,
			)
		}

		a.templates[tId] = &Template{
			Id:          tId,
			Name:        constraint.TemplateName,
			Policy:      policy,
			Description: constraint.Description,
			HowToSolve:  constraint.HowToSolve,
			UsageCount:  t.UsageCount,
		}

		updated = true
	}

	return updated, nil
}

func (a *OpaAuditor) RemoveConstraint(id string) {
	c, cFound := a.constraints[id]
	if cFound {
		t := a.templates[c.TemplateId]
		if t.UsageCount > 1 {
			t.UsageCount--
		} else {
			delete(a.templates, c.TemplateId)
		}

		delete(a.constraints, id)
		a.cache.RemoveConstraint(id)
	}
}

func (a *OpaAuditor) UpdateConstraints(constraints []*agent.Constraint) ([]string, map[string]error) {
	errorsMap := make(map[string]error)
	updated := make([]string, 0)
	for _, constraint := range constraints {
		if constraint.DeletedAt != nil {
			a.RemoveConstraint(constraint.Id)
			continue
		}
		constraintUpdated, err := a.UpdateConstraint(constraint)
		if err != nil {
			errorsMap[constraint.Id] = err
			continue
		}
		if constraintUpdated {
			updated = append(updated, constraint.Id)
			a.cache.RemoveConstraint(constraint.Id)
		}
	}

	return updated, errorsMap
}

func (a *OpaAuditor) RemoveResource(resource *unstructured.Unstructured) {
	a.cache.RemoveResource(getResourceKey(resource))
}
func (a *OpaAuditor) CheckResourceStatusWithConstraint(constraintId string, resource *unstructured.Unstructured, currentStatus agent.AuditResultStatus) bool {
	oldStatus, found := a.cache.Get(constraintId, getResourceKey(resource))
	return !found || oldStatus != currentStatus
}
func (a *OpaAuditor) UpdateCache(results []*agent.AuditResult) {
	lock.Lock()
	for i := range results {
		result := results[i]
		namespace := ""
		if result.NamespaceName != nil {
			namespace = *result.NamespaceName
		}
		kind := ""
		if result.EntityKind != nil {
			kind = *result.EntityKind
		}
		name := ""
		if result.EntityName != nil {
			name = *result.EntityName
		}
		a.cache.Put(*result.ConstraintID, kuber.GetEntityKey(namespace, kind, name), result.Status)
	}
	lock.Unlock()
}

// evaluate constraint, construct recommendation obj
func (a *OpaAuditor) Audit(resource *unstructured.Unstructured, constraintIds []string, triggerType string) ([]*agent.AuditResult, []error) {
	if len(resource.GetOwnerReferences()) > 0 {
		return nil, nil
	}
	constraints := getConstraints(constraintIds, a.constraints)
	results := make([]*agent.AuditResult, 0, len(constraints))
	errs := make([]error, 0)

	// Get resource identity info based on resource type
	namespace := resource.GetNamespace()
	kind := resource.GetKind()
	name := resource.GetName()
	parent, found := a.entitiesWatcher.GetParents(namespace, kind, name)
	var parentName, parentKind string
	if found && parent != nil {
		// Ignore audit result for pod and replicasets with parents
		if kind == "Pod" || kind == "ReplicaSet" {
			return nil, nil
		}
		// RootParent func should move outside kuber
		topParent := kuber.RootParent(parent)
		parentName = topParent.Name
		parentKind = topParent.Kind
	}

	for idx := range constraints {
		c := constraints[idx]
		templateId := c.TemplateId
		constraintId := c.Id
		categoryId := c.CategoryId
		severity := c.Severity

		match := matchEntity(resource, c.Match)
		if !match {
			continue
		} else {
			res := agent.AuditResult{
				TemplateID:    &templateId,
				ConstraintID:  &constraintId,
				CategoryID:    &categoryId,
				Severity:      &severity,
				Standards:     c.Standards,
				Controls:      c.Controls,
				Description:   a.templates[templateId].Description,
				HowToSolve:    a.templates[templateId].HowToSolve,
				EntityName:    &name,
				EntityKind:    &kind,
				NamespaceName: &namespace,
				ParentName:    &parentName,
				ParentKind:    &parentKind,
				EntitySpec:    resource.Object,
				Trigger:       triggerType,
			}
			res.GenerateID()

			t := a.templates[c.TemplateId]
			err := t.Policy.EvalGateKeeperCompliant(resource.Object, c.Parameters, PolicyQuery)
			var opaErr opa.OPAError
			if err != nil {
				if errors.As(err, &opaErr) {
					details := make(map[string]interface{})
					detailsInt := opaErr.GetDetails()
					detailsMap, ok := detailsInt.(map[string]interface{})
					if ok {
						details = detailsMap
					} else {
						details["issue"] = detailsInt
					}

					var title string
					if msg, ok := details["msg"]; ok {
						title = msg.(string)
					} else {
						title = c.Name
					}

					msg := fmt.Sprintf("%s in %s %s", title, kind, name)
					res.Status = agent.AuditResultStatusViolating
					res.Msg = &msg
				} else {
					errs = append(errs, fmt.Errorf("unable to evaluate resource against policy. template id: %s, constraint id: %s. %w", c.TemplateId, c.Id, err))
				}
			} else {
				res.Status = agent.AuditResultStatusCompliant
			}

			results = append(results, &res)

		}
	}
	return results, errs
}

func getConstraints(constraintIds []string, cachedConstraints map[string]*Constraint) map[string]*Constraint {
	var constraints map[string]*Constraint
	if constraintIds == nil || len(constraintIds) == 0 {
		constraints = cachedConstraints
	} else {
		constraints = make(map[string]*Constraint)
		for _, id := range constraintIds {
			c, ok := cachedConstraints[id]
			if ok {
				constraints[id] = c
			}
		}
	}
	return constraints
}

func matchEntity(resource *unstructured.Unstructured, match agent.Match) bool {
	var matchKind bool
	var matchNamespace bool
	var matchLabel bool

	if len(match.Kinds) == 0 {
		matchKind = true
	} else {
		resourceKind := resource.GetKind()
		for _, kind := range match.Kinds {
			if resourceKind == kind {
				matchKind = true
				break
			}
		}
	}

	if len(match.Namespaces) == 0 {
		matchNamespace = true
	} else {
		resourceNamespace := resource.GetNamespace()
		for _, namespace := range match.Namespaces {
			if resourceNamespace == namespace {
				matchNamespace = true
				break
			}
		}
	}

	if len(match.Labels) == 0 {
		matchLabel = true
	} else {
	outer:
		for _, obj := range match.Labels {
			for key, val := range obj {
				entityVal, ok := resource.GetLabels()[key]
				if ok {
					if val != "*" && val != entityVal {
						continue
					}
					matchLabel = true
					break outer
				}
			}
		}
	}

	return matchKind && matchNamespace && matchLabel
}

func getResourceKey(resource *unstructured.Unstructured) string {
	return kuber.GetEntityKey(resource.GetNamespace(), resource.GetKind(), resource.GetName())
}
