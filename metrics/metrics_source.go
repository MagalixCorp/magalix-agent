package metrics

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

type EntitiesProvider interface {
	GetNodes() ([]corev1.Node, error)
	GetPods() ([]corev1.Pod, error)
	FindController(namespaceName string, podName string) (string, string, error)
}

// in future releases. Consider using Source interface instead.
// MetricsSource interface for metrics source
type MetricsSource interface {
	GetMetrics(entitiesProvider EntitiesProvider, tickTime time.Time) ([]*Metric, map[string]interface{}, error)
}
