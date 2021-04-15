package opa_auditor

import (
	"fmt"
	"github.com/MagalixCorp/magalix-agent/v3/kuber"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"time"

	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"github.com/pkg/errors"

	opa "github.com/MagalixTechnologies/opa-core"
)

const (
	PolicyQuery = "violation"
)

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
}

type OpaAuditor struct {
	templates   map[string]*Template
	constraints map[string]*Constraint
	cache       *AuditResultsCache

	parentsStore *kuber.ParentsStore
}

func New(parentsStore *kuber.ParentsStore) *OpaAuditor {
	return &OpaAuditor{
		templates:   make(map[string]*Template),
		constraints: make(map[string]*Constraint),
		cache:       NewAuditResultsCache(),

		parentsStore: parentsStore,
	}
}

func (a *OpaAuditor) AddConstraint(constraint *agent.Constraint) (bool, error) {
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
		constraintUpdated, err := a.AddConstraint(constraint)
		if err != nil {
			errorsMap[constraint.Id] = err
			continue
		}
		if constraintUpdated {
			updated = append(updated, constraint.Id)
			a.cache.RemoveConstraint(constraint.Id)
		}
	}

	for _, info := range a.findConstraintsToDelete(constraints) {
		a.RemoveConstraint(info.Id)
	}

	return updated, errorsMap
}

func (a *OpaAuditor) findConstraintsToDelete(constraints []*agent.Constraint) []*Constraint {
	toDelete := make([]*Constraint, 0)
	constraintsMap := make(map[string]*agent.Constraint)
	for _, c := range constraints {
		constraintsMap[c.Id] = c
	}

	for id, c := range a.constraints {
		if _, found := constraintsMap[id]; !found {
			toDelete = append(toDelete, c)
		}
	}

	return toDelete
}

func (a *OpaAuditor) RemoveResource(resource *unstructured.Unstructured) {
	a.cache.RemoveResource(getResourceKey(resource))
}

func (a *OpaAuditor) Audit(resource *unstructured.Unstructured, constraintIds []string, useCache bool) ([]*agent.AuditResult, []error) {
	var constraints map[string]*Constraint
	if constraintIds == nil || len(constraintIds) == 0 {
		constraints = a.constraints
	} else {
		constraints = make(map[string]*Constraint)
		for _, id := range constraintIds {
			c, ok := a.constraints[id]
			if ok {
				constraints[id] = c
			}
		}
	}
	results := make([]*agent.AuditResult, 0, len(constraints))
	errs := make([]error, 0)

	// Get resource identity info based on resource type
	namespace := resource.GetNamespace()
	kind := resource.GetKind()
	name := resource.GetName()
	parent, found := a.parentsStore.GetParents(namespace, kind, name)
	var parentName, parentKind string
	if found && parent != nil {
		// Ignore audit result for pod with parents
		if kind == "Pod" {
			return nil, nil
		}
		// RootParent func should move outside kuber
		topParent := kuber.RootParent(parent)
		parentName = topParent.Name
		parentKind = topParent.Kind
	}

	for _, c := range constraints {
		templateId := c.TemplateId
		constraintId := c.Id
		categoryId := c.CategoryId
		severity := c.Severity

		match := matchEntity(resource, c.Match)
		if !match {
			continue
		} else {
			res := agent.AuditResult{
				TemplateID:   &templateId,
				ConstraintID: &constraintId,
				CategoryID:   &categoryId,
				Severity:     &severity,

				Description: a.templates[templateId].Description,
				HowToSolve: a.templates[templateId].HowToSolve,

				EntityName:    &name,
				EntityKind:    &kind,
				NamespaceName: &namespace,
				ParentName:    &parentName,
				ParentKind:    &parentKind,
				EntitySpec:    resource.Object,
			}

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
					errs = append(errs, fmt.Errorf("unable to evaluate resource against policy. constraint id: %s. %w", c.Id, err))
				}
			} else {
				res.Status = agent.AuditResultStatusCompliant
			}

			resourceKey := getResourceKey(resource)
			if useCache {
				oldStatus, found := a.cache.Get(c.Id, resourceKey)
				if !found || oldStatus != res.Status {
					results = append(results, &res)
				}
			} else {
				results = append(results, &res)
			}

			a.cache.Put(c.Id, resourceKey, res.Status)
		}
	}
	return results, errs
}

func matchEntity(resource *unstructured.Unstructured, match agent.Match) bool {
	var matchKind bool
	var matchNamespace bool

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
	return matchKind && matchNamespace
}

func getResourceKey(resource *unstructured.Unstructured) string {
	return kuber.GetEntityKey(resource.GetNamespace(), resource.GetKind(), resource.GetName())
}
