package kuber

import (
	"fmt"
	"github.com/reconquest/karma-go"
	apisv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Identifyable interface {
	GetOwnerReferences() []apisv1.OwnerReference
	GetNamespace() string
	GetKind() string
	GetName() string
	GetAPIVersion() string
}

type GetWatcherFromKindFunc func(kind string) (Watcher, bool)

type ParentController struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	APIVersion string `json:"api_version"`
	IsWatched  bool   `json:"is_watched"`

	Parent *ParentController `json:"parent"`
}

func GetParents(
	obj Identifyable,
	getWatcher GetWatcherFromKindFunc,
) (*ParentController, error) {
	ctx := karma.
		Describe("object_name", obj.GetName()).
		Describe("object_kind", obj.GetKind()).
		Describe("object_namespace", obj.GetNamespace()).
		Describe("object_api_version", obj.GetAPIVersion())

	owners := obj.GetOwnerReferences()

	if len(owners) > 1 {

		for i, owner := range owners {
			ctx = ctx.
				Describe(fmt.Sprintf("owner_name_%d", i), owner.Name).
				Describe(fmt.Sprintf("owner_kind_%d", i), owner.Kind).
				Describe(fmt.Sprintf("owner_api_version_%d", i), owner.APIVersion)
		}

		return nil, ctx.Format(nil, "object has multiple owners")
	}

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

			parent.IsWatched = true

			ownerObj, err := watcher.Lister().
				ByNamespace(obj.GetNamespace()).
				Get(owner.Name)
			if err != nil {
				return nil, ctx.Format(
					err,
					"unable to get parent owner",
				)
			}
			ownerU, ok := ownerObj.(*unstructured.Unstructured)
			if !ok {
				return nil, ctx.Format(
					nil,
					"unable to cast runtime.Object to *unstructured.Unstructured",
				)
			}
			parentParent, err := GetParents(ownerU, getWatcher)
			if err != nil {
				return nil, ctx.Format(
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

	for p.Parent != nil {
		p = parent.Parent
	}

	return p
}
