package agent

import (
	"context"
	"time"
)

// Metric metrics struct
type Metric struct {
	Name           string
	Type           string
	NodeName       string
	NodeIP         string
	NamespaceName  string
	ControllerName string
	ControllerKind string
	ContainerName  string
	Timestamp      time.Time
	Value          int64
	PodName        string

	AdditionalTags map[string]interface{}
}

type MetricsHandler func([]*Metric) error

type MetricsSource interface {
	Start(ctx context.Context) error
	Stop() error

	SetMetricsHandler(handler MetricsHandler)
}