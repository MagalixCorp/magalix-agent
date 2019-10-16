package metrics

import (
	"github.com/MagalixCorp/magalix-agent/scanner"
	"time"
)

// in future releases. Consider using Source interface instead.
// MetricsSource interface for metrics source
type MetricsSource interface {
	GetMetrics(scanner *scanner.Scanner, tickTime time.Time) ([]*Metrics, map[string]interface{}, error)
}
