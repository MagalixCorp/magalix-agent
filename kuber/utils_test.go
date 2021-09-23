package kuber

import (
	"testing"
)

func TestParentStore(t *testing.T) {
	deployment := ParentController{
		Kind:       "Deployment",
		Name:       "myapp",
		APIVersion: "v1",
	}

	parentStore := NewParentsStore()
	parentStore.SetParents("default", "Pod", "myapp", &deployment)

	parent, _ := parentStore.GetParents("default", "Pod", "myapp")
	if parent == nil {
		t.Errorf("getting parents does not work")
	} else {
		if parent.Kind != "Deployment" {
			t.Errorf("expected parent with kind Deployment, found kind %s", parent.Kind)

		}
	}

	parentStore.Delete("default", "Pod", "myapp")
	parent, _ = parentStore.GetParents("default", "Pod", "myapp")
	if parent != nil {
		t.Errorf("delete parent doesn't work")
	}
}

func TestGetRootParent(t *testing.T) {
	deployment := ParentController{
		Kind:       "Deployment",
		Name:       "myapp",
		APIVersion: "v1",
	}

	statfulset := ParentController{
		Kind:       "StatfulSet",
		Name:       "myapp",
		APIVersion: "v1",
		Parent:     &deployment,
	}

	pod := ParentController{
		Kind:       "Pod",
		Name:       "myapp",
		APIVersion: "v1",
		Parent:     &statfulset,
	}

	parent := RootParent(&pod)
	if parent == nil {
		t.Errorf("getting root parent does not work")
	} else {
		if parent.Kind != "Deployment" {
			t.Errorf("expected root parent with kind Deployment, found kind %s", parent.Kind)
		}
	}
}
