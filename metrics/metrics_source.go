package metrics

import (
	"github.com/MagalixCorp/magalix-agent/scanner"
	"time"
)

// MetricsSource interface for metrics source
type MetricsSource interface {
	GetMetrics(scanner *scanner.Scanner) ([]*Metrics, map[string]interface{}, error)
}

type Source interface {
	GetMetrics(time time.Time) (*MetricsBatch, error)
}
