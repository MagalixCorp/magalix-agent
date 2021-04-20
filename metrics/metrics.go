package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"github.com/MagalixCorp/magalix-agent/v3/kuber"
	"github.com/MagalixTechnologies/core/logger"
)

type Metrics struct {
	source          MetricsSource
	metricsInterval time.Duration
	cancelWorker    context.CancelFunc
	sendMetrics     agent.MetricsHandler
}

func NewMetrics(
	entitiesProvider EntitiesProvider,
	kube *kuber.Kube,
	kubeletPort string,
	metricsInterval time.Duration,
	kubeletBackoffSleepTime time.Duration,
	kubeletBackoffMaxRetries int,
) (*Metrics, error) {
	kubeletClient, err := NewKubeletClient(entitiesProvider, kube, kubeletPort)
	if err != nil {
		return nil, fmt.Errorf("error getting new Kubelet client for metrics: %w", err)
	}

	kubelet, err := NewKubelet(
		kubeletClient,
		entitiesProvider,
		kubeletBackoffSleepTime,
		kubeletBackoffMaxRetries,
	)
	if err != nil {
		return nil, fmt.Errorf("unable to initialize kubelet source, error: %w", err)
	}

	return &Metrics{
		source:          kubelet,
		metricsInterval: metricsInterval,
	}, nil
}

func (m *Metrics) SetMetricsHandler(handler agent.MetricsHandler) {
	m.sendMetrics = handler
}

func (m *Metrics) Start(ctx context.Context) error {
	if m.cancelWorker != nil {
		m.cancelWorker()
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	m.cancelWorker = cancel

	ticker := time.NewTicker(m.metricsInterval)

	logger.Debug("Metrics worker started")

	for {
		select {
		case <-cancelCtx.Done():
			ticker.Stop()
			logger.Debug("Metrics worker stopped")
			return nil
		case <-ticker.C:
			metrics, err := m.source.GetMetrics()
			if err != nil {
				logger.Errorf("failed to get metrics. %w", err)
				continue
			}

			err = m.sendMetrics(metrics)
			if err != nil {
				logger.Errorf("failed to send metrics. %w", err)
			}
		}
	}
}

func (m *Metrics) Stop() error {
	if m.cancelWorker == nil {
		return nil
	}

	m.cancelWorker()
	return nil
}
