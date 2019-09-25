package kuber

import (
	"github.com/reconquest/karma-go"
	apisv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Identifyable interface {
	GetOwnerReferences() []apisv1.OwnerReference
	GetNamespace() string
}

type GetWatcherFromKindFunc func(kind string) (Watcher, bool)

type ParentController struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	APIVersion string `json:"api_version"`

	Parent *ParentController `json:"parent"`
}

func GetParents(
	obj Identifyable,
	getWatcher GetWatcherFromKindFunc,
) (*ParentController, error) {
	owners := obj.GetOwnerReferences()
	var parent *ParentController
	for _, owner := range owners {
		if owner.Controller != nil && *owner.Controller {
			parent = &ParentController{
				Kind:       owner.Kind,
				Name:       owner.Name,
				APIVersion: owner.APIVersion,
			}

			watcher, ok := getWatcher(owner.Kind)
			if !ok {
				// not watched parent
				break
			}
			ownerObj, err := watcher.Lister().
				ByNamespace(obj.GetNamespace()).
				Get(owner.Name)
			if err != nil {
				return nil, karma.Format(
					err,
					"unable to get parent owner",
				)
			}
			ownerU, ok := ownerObj.(*unstructured.Unstructured)
			if !ok {
				return nil, karma.Format(
					nil,
					"unable to cast runtime.Object to *unstructured.Unstructured",
				)
			}
			parentParent, err := GetParents(ownerU, getWatcher)
			if err != nil {
				return nil, karma.Format(
					err,
					"unable to get parent.parent",
				)
			}
			parent.Parent = parentParent
		}
	}
	return parent, nil
}

func RootParent(parent *ParentController) *ParentController {
	if parent == nil {
		return nil
	}

	p := parent

	for parent.Parent != nil {
		p = parent.Parent
	}

	return p
}
