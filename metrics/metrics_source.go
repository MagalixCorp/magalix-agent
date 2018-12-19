package metrics

import (
	"github.com/MagalixCorp/magalix-agent/scanner"
)

// MetricsSource interface for metrics source
type MetricsSource interface {
	GetMetrics(scanner *scanner.Scanner) ([]*Metrics, map[string]interface{}, error)
}
