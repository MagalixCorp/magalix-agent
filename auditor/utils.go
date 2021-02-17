package auditor

import (
	"encoding/json"
	"fmt"

	"github.com/MagalixCorp/magalix-agent/v2/agent"
	"github.com/MagalixCorp/magalix-agent/v2/auditor/target"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/MagalixTechnologies/uuid-go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	opaTemplates "github.com/open-policy-agent/frameworks/constraint/pkg/core/templates"
	ks8CoreV1 "k8s.io/api/core/v1"
	k8sMetaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

func forceDns1035Compatible(id string) string {
	return "id-" + id
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

func convertMgxConstraintToOpaTemplateAndConstraint(constraint *agent.Constraint) (
	*opaTemplates.ConstraintTemplate,
	*unstructured.Unstructured,
	error,
) {
	validTemplateName := forceDns1035Compatible(constraint.TemplateId.String())
	validConstraintName := forceDns1035Compatible(constraint.Id.String())

	opaTemplate := opaTemplates.ConstraintTemplate{
		TypeMeta: k8sMetaV1.TypeMeta{
			Kind:       CrdKindConstraintTemplate,
			APIVersion: CrdAPIVersionGkTemplateV1Beta1,
		},
		ObjectMeta: k8sMetaV1.ObjectMeta{
			Name: validTemplateName,
			UID:  k8sTypes.UID(constraint.TemplateId.String()),
			Annotations: map[string]string{
				AnnotationKeyTemplateId:   constraint.TemplateId.String(),
				AnnotationKeyTemplateName: constraint.TemplateName,
			},
		},
		Spec: opaTemplates.ConstraintTemplateSpec{
			CRD: opaTemplates.CRD{
				Spec: opaTemplates.CRDSpec{
					Names: opaTemplates.Names{
						Kind: validTemplateName,
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

	opaConstraint := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": CrdAPIVersionGkConstraintV1Beta1,
			"kind":       validTemplateName,
			"metadata": map[string]interface{}{
				"name": validConstraintName,
				"uid":  constraint.Id.String(),
				"annotations": map[string]interface{}{
					AnnotationKeyTemplateId:     constraint.TemplateId.String(),
					AnnotationKeyTemplateName:   constraint.TemplateName,
					AnnotationKeyConstraintId:   constraint.Id.String(),
					AnnotationKeyConstraintName: constraint.Name,
				},
			},
			"spec": map[string]interface{}{
				"match": map[string]interface{}{
					"kinds":      convertKindsListToKindsMatcher(constraint.Match.Kinds),
					"namespaces": convertNamespacesListToNamespacesMatcher(constraint.Match.Namespaces),
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

func convertKindsListToKindsMatcher(kinds []string) []interface{} {
	matchedKinds := make([]interface{}, 0, len(kinds))

	if len(kinds) == 0 {
		matchedKinds = []interface{}{"*"}
	} else {
		for _, k := range kinds {
			matchedKinds = append(matchedKinds, k)
		}
	}

	return []interface{}{map[string]interface{}{
		"apiGroups": []interface{}{"*"},
		"kinds":     matchedKinds,
	}}
}

func convertNamespacesListToNamespacesMatcher(namespaces []string) []interface{} {
	if len(namespaces) == 0 {
		return []interface{}{"*"}
	}

	matchedNamespaces := make([]interface{}, 0, len(namespaces))
	for i, ns := range namespaces {
		matchedNamespaces[i] = ns
	}

	return matchedNamespaces
}

func getNodeIpFromUnstructured(node *unstructured.Unstructured) (string, error) {
	addresses, found, err := unstructured.NestedSlice(node.Object, "status", "addresses")
	if err != nil || !found {
		logger.Errorf("couldn't get node addresses. %w")
	}

	for _, addr := range addresses {
		addrMap, ok := addr.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("couldn't cast node address to map[string]interface{}")
		}

		if ipType := addrMap["type"]; ipType == ks8CoreV1.NodeInternalIP {
			return addrMap["address"].(string), nil
		}
	}

	return "", fmt.Errorf("couldn't find node ip")
}