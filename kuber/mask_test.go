package kuber

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestMasking(t *testing.T) {
	resource := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name": "myapp",
		},
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"initContainers": []interface{}{
						map[string]interface{}{
							"name": "container-1",
							"env": []interface{}{
								map[string]interface{}{
									"name":  "username",
									"value": "test",
								},
								map[string]interface{}{
									"name":  "password",
									"value": "test",
								},
							},
							"args": []interface{}{
								"arg1",
								"arg2",
							},
						},
						map[string]interface{}{
							"name": "container-2",
							"env": []interface{}{
								map[string]interface{}{
									"name":  "username",
									"value": "test",
								},
								map[string]interface{}{
									"name":  "password",
									"value": "test",
								},
							},
							"args": []interface{}{
								"arg1",
								"arg2",
							},
						},
					},
					"containers": []interface{}{
						map[string]interface{}{
							"name": "container-1",
							"env": []interface{}{
								map[string]interface{}{
									"name":  "username",
									"value": "test",
								},
								map[string]interface{}{
									"name":  "password",
									"value": "test",
								},
							},
							"args": []interface{}{
								"arg1",
								"arg2",
							},
						},
						map[string]interface{}{
							"name": "container-2",
							"env": []interface{}{
								map[string]interface{}{
									"name":  "username",
									"value": "test",
								},
								map[string]interface{}{
									"name":  "password",
									"value": "test",
								},
							},
							"args": []interface{}{
								"arg1",
								"arg2",
							},
						},
					},
				},
			},
		},
	}

	maskedResource, err := maskUnstructured(&unstructured.Unstructured{Object: resource})
	if err != nil {
		t.Error("mask", err)
	}

	for _, containersKey := range []string{"containers", "initContainers"} {
		containers, _, err := unstructured.NestedSlice(maskedResource.Object, "spec", "template", "spec", containersKey)
		if err != nil {
			t.Error("slice", err)
		}

		for i := range containers {
			container := containers[i].(map[string]interface{})

			for _, arg := range container["args"].([]interface{}) {
				if arg != maskedValue {
					t.Errorf("[%s] container argument is not masked", containersKey)
				}
			}

			for _, env := range container["env"].([]interface{}) {
				if env.(map[string]interface{})["value"] != maskedValue {
					t.Errorf("[%s] container environment value is not masked", containersKey)
				}
			}
		}
	}
}
