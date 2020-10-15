package metrics

import (
	"fmt"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/client"
	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
)

const limit = 1000

type Entities struct {
	Node        *uuid.UUID
	Application *uuid.UUID
	Service     *uuid.UUID
	Container   *uuid.UUID
}

type RawMetric struct {
	Metric string

	Account uuid.UUID
	Cluster uuid.UUID
	Node    uuid.UUID

	Application *uuid.UUID
	Service     *uuid.UUID
	Container   *uuid.UUID

	Tags  map[string]string
	Value float64

	Timestamp time.Time
}

type MetricFamily struct {
	Name string
	Help string
	Type string
	Tags []string

	Values []*MetricValue
}

type MetricValue struct {
	*Entities

	Tags  map[string]string
	Value float64
}

type MetricsBatch struct {
	Timestamp time.Time

	Metrics map[string]*MetricFamily
}

// map of metric_name:list of metric points
type RawMetrics []*RawMetric

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

const (
	// TypeCluster cluster
	TypeCluster = "cluster"
	// TypeNode node
	TypeNode = "node"
	// TypePod pod
	TypePod = "pod"
	// TypePodContainer container in a pod
	TypePodContainer = "pod_container"
	// TypeSysContainer system container
	TypeSysContainer = "sys_container"
)

// Please consider using watchMetricsProm instead.
func watchMetrics(
	client *client.Client,
	source MetricsSource,
	entitiesProvider EntitiesProvider,
	interval time.Duration,
) {
	metricsPipe := make(chan []*Metric)
	go sendMetrics(client, metricsPipe)
	defer close(metricsPipe)

	ticker := utils.NewTicker("metrics", interval, func(tickTime time.Time) {
		logger.Info("Retrieving metrics")
		metrics, err := source.GetMetrics(entitiesProvider, tickTime)

		if err != nil {
			logger.Errorw("unable to retrieve metrics from sink", "error", err)
		}

		if len(metrics) > 0 {
			logger.Infow("finished retrieving metrics", "timestamp", metrics[0].Timestamp)

			for i := 0; i < len(metrics); i += limit {
				metricsPipe <- metrics[i:min(i+limit, len(metrics))]
			}
		}
	})
	ticker.Start(false, true, true)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func sendMetrics(client *client.Client, pipe chan []*Metric) {
	queueLimit := 100
	queue := make(chan []*Metric, queueLimit)
	defer close(queue)
	go func() {
		for metrics := range queue {
			if len(metrics) > 0 {
				logger.Debugw("sending metrics", "timestamp", metrics[0].Timestamp)
				sendMetricsBatch(client, metrics)
				logger.Infow(fmt.Sprintf("%d metrics sent", len(metrics)), "timestamp", metrics[0].Timestamp)
			}
		}
	}()
	for metrics := range pipe {
		if len(queue) >= queueLimit-1 {
			// Discard the oldest value
			<-queue
		}
		queue <- metrics
	}
}

// SendMetrics bulk send metrics
func sendMetricsBatch(c *client.Client, metrics []*Metric) {
	var packet interface{}
	var packetKind proto.PacketKind

	var req proto.PacketMetricsStoreV2Request
	for _, metric := range metrics {
		req = append(req, proto.MetricStoreV2Request{
			Name:           metric.Name,
			Type:           metric.Type,
			NodeName:       metric.NodeName,
			NodeIP:         metric.NodeIP,
			NamespaceName:  metric.NamespaceName,
			ControllerName: metric.ControllerName,
			ControllerKind: metric.ControllerKind,
			ContainerName:  metric.ContainerName,
			Timestamp:      metric.Timestamp,
			Value:          metric.Value,
			PodName:        metric.PodName,
			AdditionalTags: metric.AdditionalTags,
		})

	}
	packet = req
	packetKind = proto.PacketKindMetricsStoreV2Request

	c.Pipe(client.Package{
		Kind:        packetKind,
		ExpiryTime:  utils.After(2 * time.Hour),
		ExpiryCount: 100,
		Priority:    4,
		Retries:     10,
		Data:        packet,
	})
}

// InitMetrics init metrics source
func InitMetrics(
	client *client.Client,
	nodesProvider NodesProvider,
	entitiesProvider EntitiesProvider,
	kube *kuber.Kube,
	args map[string]interface{},
) error {
	var (
		metricsInterval = utils.MustParseDuration(args, "--metrics-interval")
		failOnError     = false // whether the agent will fail to start if an error happened during init metric source

		metricsSources = []MetricsSource{}
		foundErrors    = make([]error, 0)
	)

	metricsSourcesNames := []string{"kubelet"}
	if names, ok := args["--source"].([]string); ok && len(names) > 0 {
		metricsSourcesNames = names
		failOnError = true
	}

	kubeletClient, err := NewKubeletClient(nodesProvider, kube, args)
	if err != nil {
		foundErrors = append(foundErrors, err)
		failOnError = true
	}

	for _, metricsSource := range metricsSourcesNames {
		switch metricsSource {
		case "kubelet":
			kubelet, err := NewKubelet(
				kubeletClient,
				metricsInterval,
				kubeletTimeouts{
					backoff: backOff{
						sleep:      utils.MustParseDuration(args, "--kubelet-backoff-sleep"),
						maxRetries: utils.MustParseInt(args, "--kubelet-backoff-max-retries"),
					},
				},
			)
			if err != nil {
				foundErrors = append(foundErrors, karma.Format(
					err,
					"unable to initialize kubelet source",
				))
				continue
			}

			metricsSources = append(metricsSources, kubelet)
		}
	}

	if len(foundErrors) > 0 && (failOnError || len(metricsSources) == 0) {
		return karma.Format(foundErrors, "unable to init metric sources")
	}

	for _, source := range metricsSources {
		go watchMetrics(
			client,
			source,
			entitiesProvider,
			metricsInterval,
		)
	}

	return nil
}
