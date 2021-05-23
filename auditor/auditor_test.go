package auditor

import (
	"context"
	"testing"
	"time"

	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"github.com/MagalixCorp/magalix-agent/v3/kuber"
	"github.com/MagalixCorp/magalix-agent/v3/tests/mocks"
	"github.com/MagalixTechnologies/uuid-go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestConstraintsCache(t *testing.T) {
	results := []*agent.AuditResult{}

	entities := map[kuber.GroupVersionResourceKind][]unstructured.Unstructured{
		kuber.Deployments: {
			{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "deployment-1",
						"labels": map[string]interface{}{
							"app":   "deployment-1",
							"owner": "bob",
						},
					},
				},
			},
			{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "deployment-2",
						"labels": map[string]interface{}{
							"app": "deployment-2",
						},
					},
				},
			},
		},
		kuber.StatefulSets: {
			{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "StatefulSet",
					"metadata": map[string]interface{}{
						"name": "deployment-2",
						"labels": map[string]interface{}{
							"app": "deployment-2",
						},
					},
				},
			},
		},
	}

	parents := map[string]*kuber.ParentController{
		kuber.GetEntityKey("", "StatefulSet", "deployment-2"): {
			Kind:       "Deployment",
			Name:       "deployment-2",
			APIVersion: "apps/v1",
		},
	}

	ew := mocks.EntitiesWatcherMock{Entities: entities, Parents: parents}

	aud := NewAuditor(&ew)
	aud.SetAuditResultHandler(func(auditResult []*agent.AuditResult) error {
		results = append(results, auditResult...)
		return nil
	})

	go aud.Start(context.Background())
	defer aud.Stop()

	aud.HandleConstraints([]*agent.Constraint{
		{
			TemplateId:   uuid.NewV4().String(),
			TemplateName: uuid.NewV4().String(),
			Id:           uuid.NewV4().String(),
			Name:         uuid.NewV4().String(),
			ClusterId:    uuid.NewV4().String(),
			AccountId:    uuid.NewV4().String(),
			CategoryId:   uuid.NewV4().String(),
			UpdatedAt:    time.Now(),
			Code:         "package magalix.advisor.labels.missing_label\n\nlabel := input.parameters.label\n\nviolation[result] {\n  not input.review.object.metadata.labels[label]\n  result = {\n    \"issue detected\": true,\n    \"msg\": sprintf(\"you are missing a label with the key '%v'\", [label]),   \n  }\n}\n\n",
			Parameters:   map[string]interface{}{"label": "owner"},
		},
	})

	time.Sleep(3 * time.Second)

	for _, r := range results {
		if *r.EntityName == "deployment-1" && r.Status != "Compliance" {
			t.Errorf("expected deployment-1 status to be Compliance, found %s", r.Status)
		}

		if *r.EntityName == "deployment-2" && *r.EntityKind == "Deployment" && r.Status != "Violation" {
			t.Errorf("expected deployment deployment-2 status to be Violation, found %s", r.Status)
		}

		if *r.EntityName == "deployment-2" && *r.EntityKind == "StatefulSet" && r.Status != "Violation" {
			t.Errorf("expected statefulset deployment-2 status to be Violation, found %s", r.Status)
		}
	}
}
