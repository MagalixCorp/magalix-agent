package agent

import (
	"context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"time"
)

const (
	EntityDeltaKindUpsert EntityDeltaKind = "UPSERT"
	EntityDeltaKindDelete EntityDeltaKind = "DELETE"
)

type EntityDeltaKind string

type GroupVersionResourceKind struct {
	schema.GroupVersionResource
	Kind string
}

type ParentController struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	APIVersion string `json:"api_version"`
	IsWatched  bool   `json:"is_watched"`

	Parent *ParentController `json:"parent"`
}

type Delta struct {
	Kind      EntityDeltaKind
	Gvrk      GroupVersionResourceKind
	Data      unstructured.Unstructured
	Parent    *ParentController
	Timestamp time.Time
}

type EntitiesResyncItem struct {
	Gvrk GroupVersionResourceKind     `json:"gvrk"`
	Data []*unstructured.Unstructured `json:"data"`
}

type EntitiesResync struct {
	Timestamp time.Time `json:"timestamp"`

	// map of entities kind and entities definitions
	Snapshot map[string]EntitiesResyncItem `json:"snapshot"`
}

type DeltasHandler func(deltas []*Delta) error
type EntitiesResyncHandler func(resync *EntitiesResync) error

type EntitiesSource interface {
	Start(ctx context.Context) error
	Stop() error

	SetDeltasHandler(handler DeltasHandler)
	SetEntitiesResyncHandler(handler EntitiesResyncHandler)
}
