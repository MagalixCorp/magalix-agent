package agent

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type EntityDeltaKind string

const (
	EntityDeltaKindUpsert EntityDeltaKind = "UPSERT"
	EntityDeltaKindDelete EntityDeltaKind = "DELETE"
)

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

type EntitiesSource interface {
	Start(ctx context.Context) error
	Stop() error
}
