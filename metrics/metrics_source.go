package metrics

import "github.com/MagalixTechnologies/agent/scanner"

// MetricsSource interface for metrics source
type MetricsSource interface {
	GetMetrics(scanner *scanner.Scanner) ([]*Metrics, error)
}
