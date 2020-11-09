package kuber

import (
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	apisv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Identifiable interface {
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

// TODO: Extract into a dependency
type ParentsStore struct {
	parents map[string]*ParentController
	sync.Mutex
}

func (s *ParentsStore) SetParents(namespace string, kind string, name string, parent *ParentController) {
	s.Lock()
	defer s.Unlock()
	s.parents[GetEntityKey(namespace, kind, name)] = parent
}

func (s *ParentsStore) GetParents(namespace string, kind string, name string) (*ParentController, bool) {
	parents, found := s.parents[GetEntityKey(namespace, kind, name)]
	return parents, found
}

func (s *ParentsStore) Delete(namespace string, kind string, name string) {
	s.Lock()
	defer s.Unlock()

	delete(s.parents, GetEntityKey(namespace, kind, name))
}

func NewParentsStore() *ParentsStore {
	return &ParentsStore{
		parents: make(map[string]*ParentController),
		Mutex:   sync.Mutex{},
	}
}

func GetEntityKey(namespace string, kind string, name string) string {
	return fmt.Sprintf("%s:%s:%s", namespace, kind, name)
}

func GetParents(
	obj Identifiable,
	parentsStore *ParentsStore,
	getWatcher GetWatcherFromKindFunc,
) (*ParentController, error) {
	errMap := map[string]interface{}{
		"object_name":        obj.GetName(),
		"object_kind":        obj.GetKind(),
		"object_namespace":   obj.GetNamespace(),
		"object_api_version": obj.GetAPIVersion(),
	}

	parents, found := parentsStore.GetParents(obj.GetNamespace(), obj.GetKind(), obj.GetName())
	if found {
		return parents, nil
	}

	owners := obj.GetOwnerReferences()

	if len(owners) > 1 {

		for i, owner := range owners {
			errMap[fmt.Sprintf("owner_name_%d", i)] = owner.Name
			errMap[fmt.Sprintf("owner_kind_%d", i)] = owner.Kind
			errMap[fmt.Sprintf("owner_api_version_%d", i)] = owner.APIVersion
		}

		return nil, fmt.Errorf("object has multiple owners with data: %+v", errMap)
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
				return nil, fmt.Errorf(
					"unable to get parent owner, error: %w with data %+v", err, errMap,
				)
			}
			ownerU, ok := ownerObj.(*unstructured.Unstructured)
			if !ok {
				return nil, fmt.Errorf(
					"unable to cast runtime.Object to *unstructured.Unstructured with data %+v", errMap,
				)
			}
			parentParent, err := GetParents(ownerU, parentsStore, getWatcher)
			if err != nil {
				return nil, fmt.Errorf(
					"unable to get parent.parent, error: %w with data %+v", err, errMap,
				)
			}
			parent.Parent = parentParent
		}
	}

	parentsStore.SetParents(obj.GetNamespace(), obj.GetKind(), obj.GetName(), parent)

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
