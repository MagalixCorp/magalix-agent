package mocks

import (
	"github.com/MagalixCorp/magalix-agent/v3/entities"
	"github.com/MagalixCorp/magalix-agent/v3/kuber"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type EntitiesWatcherMock struct {
	Entities map[kuber.GroupVersionResourceKind][]unstructured.Unstructured
	Parents  map[string]*kuber.ParentController
}

func (ew *EntitiesWatcherMock) GetAllEntitiesByGvrk() (map[kuber.GroupVersionResourceKind][]unstructured.Unstructured, []error) {
	return ew.Entities, nil
}

func (ew *EntitiesWatcherMock) GetParents(namespace string, kind string, name string) (*kuber.ParentController, bool) {
	parents, found := ew.Parents[kuber.GetEntityKey(namespace, kind, name)]
	return parents, found
}

func (ew *EntitiesWatcherMock) AddResourceEventsHandler(handler entities.ResourceEventsHandler) {}
