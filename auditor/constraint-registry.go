package auditor

import (
	"fmt"
	"time"

	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	opaTemplates "github.com/open-policy-agent/frameworks/constraint/pkg/core/templates"
	k8sV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sSchema "k8s.io/apimachinery/pkg/runtime/schema"
)

type ConstrainInfo struct {
	Id         string
	TemplateId string
	UpdatedAt  time.Time
}

func (i *ConstrainInfo) ToOpaTemplate() *opaTemplates.ConstraintTemplate {
	return &opaTemplates.ConstraintTemplate{ObjectMeta: k8sV1.ObjectMeta{Name: i.TemplateId}}
}

func (i *ConstrainInfo) ToOpaConstraint() *unstructured.Unstructured {
	constraint := unstructured.Unstructured{}
	constraint.SetName(forceDns1035Compatible(i.Id))
	constraint.SetGroupVersionKind(k8sSchema.GroupVersionKind{
		Version: CrdAPIVersionGkConstraintV1Beta1,
		Kind:    forceDns1035Compatible(i.TemplateId),
	})

	return &constraint
}

type ConstraintRegistry struct {
	constraintsInfo map[string]*ConstrainInfo
	templatesInfo   map[string]int
}

func NewConstraintRegistry() *ConstraintRegistry {
	return &ConstraintRegistry{
		constraintsInfo: make(map[string]*ConstrainInfo),
		templatesInfo:   make(map[string]int),
	}
}

func (r *ConstraintRegistry) RegisterConstraint(c *agent.Constraint) error {
	if c == nil {
		return fmt.Errorf("trying to register a nil constraint")
	}

	_, exists := r.constraintsInfo[c.Id.String()]
	r.constraintsInfo[c.Id.String()] = &ConstrainInfo{
		Id:         c.Id.String(),
		TemplateId: c.TemplateId.String(),
		UpdatedAt:  c.UpdatedAt,
	}
	if !exists {
		r.templatesInfo[c.TemplateId.String()]++
	}

	return nil
}

func (r *ConstraintRegistry) UnregisterConstraint(c *ConstrainInfo) error {
	if c == nil {
		return fmt.Errorf("trying to unregister a nil constraint")
	}

	delete(r.constraintsInfo, c.Id)
	r.templatesInfo[c.TemplateId]--

	return nil
}

func (r *ConstraintRegistry) CheckConstraint(c *agent.Constraint) (*ConstrainInfo, bool, error) {
	if c == nil {
		return nil, false, fmt.Errorf("trying to check a nil constraint")
	}

	info, found := r.constraintsInfo[c.Id.String()]
	return info, found, nil
}

func (r *ConstraintRegistry) ShouldUpdate(c *agent.Constraint) (bool, error) {
	info, found, err := r.CheckConstraint(c)
	if err != nil {
		return false, err
	}

	if !found || c.UpdatedAt.After(info.UpdatedAt) {
		return true, nil
	}

	return false, nil
}

func (r *ConstraintRegistry) ShouldDeleteTemplate(templateId string) (bool, error) {
	count, ok := r.templatesInfo[templateId]
	if !ok {
		return false, fmt.Errorf("Tempalate: %s not found in agent cache", templateId)
	}
	return count == 0, nil
}

func (r *ConstraintRegistry) UnregisterTemplate(tempateId string) {
	delete(r.templatesInfo, tempateId)
}

func (r *ConstraintRegistry) FindConstraintsToDelete(constraints []*agent.Constraint) []*ConstrainInfo {
	toDelete := make([]*ConstrainInfo, 0, len(constraints))
	constraintsMap := make(map[string]*agent.Constraint)
	for _, c := range constraints {
		constraintsMap[c.Id.String()] = c
	}

	for id, info := range r.constraintsInfo {
		if _, found := constraintsMap[id]; !found {
			toDelete = append(toDelete, info)
		}
	}

	return toDelete
}
