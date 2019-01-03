package metrics

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/client"
	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixCorp/magalix-agent/proto"
	"github.com/MagalixCorp/magalix-agent/scanner"
	"github.com/MagalixCorp/magalix-agent/utils"
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

	Metrics []*MetricFamily
}

// map of metric_name:list of metric points
type RawMetrics []*RawMetric

// Metrics metrics struct
type Metrics struct {
	Name        string
	Type        string
	Node        uuid.UUID
	Application uuid.UUID
	Service     uuid.UUID
	Container   uuid.UUID
	Timestamp   time.Time
	Value       int64
	PodName     string

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

func watchMetrics(
	client *client.Client,
	source MetricsSource,
	scanner *scanner.Scanner,
	interval time.Duration,
) {
	metricsPipe := make(chan []*Metrics)
	go sendMetrics(client, metricsPipe)
	defer close(metricsPipe)

	ticker := utils.NewTicker("metrics", interval, func() {
		metrics, raw, err := source.GetMetrics(scanner)
		if err != nil {
			client.Errorf(err, "unable to retrieve metrics from sink")
		}
		client.Infof(karma.Describe("timestamp", metrics[0].Timestamp), "finished getting metrics")

		for i := 0; i < len(metrics); i += limit {
			metricsPipe <- metrics[i:min(i+limit, len(metrics))]
		}

		if raw != nil {
			client.SendRaw(map[string]interface{}{
				"metrics": raw,
			})
		}
	})
	ticker.Start(false, true, true)
}

func watchMetrics2(
	client *client.Client,
	source Source,
	interval time.Duration,
) {
	metricsPipe := make(chan *MetricsBatch)
	go sendMetrics2(client, metricsPipe)
	defer close(metricsPipe)

	ticker := utils.NewTicker("raw-metrics", interval, func() {
		metricsBatch, err := source.GetMetrics2()
		if err != nil {
			client.Errorf(err, "unable to retrieve metricsBatch from sink")
		}

		length := len(metricsBatch.Metrics)
		for i := 0; i < length; i += limit {
			metricsPipe <- &MetricsBatch{
				Timestamp: metricsBatch.Timestamp,
				Metrics:   metricsBatch.Metrics[i:min(i+limit, length)],
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

func sendMetrics(client *client.Client, pipe chan []*Metrics) {
	queueLimit := 100
	queue := make(chan []*Metrics, queueLimit)
	defer close(queue)
	go func() {
		for metrics := range queue {
			if len(metrics) > 0 {
				client.Infof(karma.Describe("timestamp", metrics[0].Timestamp), "sending metrics")
				sendMetricsBatch(client, metrics)
				client.Infof(karma.Describe("timestamp", metrics[0].Timestamp), "metrics sent")
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

func sendMetrics2(c *client.Client, pipe chan *MetricsBatch) {
	queueLimit := 100
	queue := make(chan *MetricsBatch, queueLimit)
	defer close(queue)
	go func() {
		for batch := range queue {
			if batch == nil {
				continue
			}
			c.Pipe(client.Package{
				Kind:        proto.PacketKindMetricsStoreRequest2,
				ExpiryTime:  utils.After(2 * time.Hour),
				ExpiryCount: 100,
				Priority:    4,
				Retries:     10,
				Data:        batch,
			})
		}
	}()
	for batch := range pipe {
		if len(queue) >= queueLimit-1 {
			// Discard the oldest value
			<-queue
		}
		queue <- batch
	}
}

// SendMetrics bulk send metrics
func sendMetricsBatch(c *client.Client, metrics []*Metrics) {
	var req proto.PacketMetricsStoreRequest
	for _, metrics := range metrics {
		req = append(req, proto.MetricStoreRequest{
			Name:        metrics.Name,
			Type:        metrics.Type,
			Node:        metrics.Node,
			Application: metrics.Application,
			Service:     metrics.Service,
			Container:   metrics.Container,
			Timestamp:   metrics.Timestamp,
			Value:       metrics.Value,
			Pod:         metrics.PodName,

			AdditionalTags: metrics.AdditionalTags,
		})

	}
	c.Pipe(client.Package{
		Kind:        proto.PacketKindMetricsStoreRequest,
		ExpiryTime:  utils.After(2 * time.Hour),
		ExpiryCount: 100,
		Priority:    4,
		Retries:     10,
		Data:        req,
	})
}

// InitMetrics init metrics source
func InitMetrics(
	client *client.Client,
	scanner *scanner.Scanner,
	kube *kuber.Kube,
	optInAnalysisData bool,
	args map[string]interface{},
) ([]MetricsSource, error) {
	var (
		metricsInterval = utils.MustParseDuration(args, "--metrics-interval")
		failOnError     = false // whether the agent will fail to start if an error happened during init metric source

		metricsSources = make([]MetricsSource, 0)
		foundErrors    = make([]error, 0)
	)

	metricsSourcesNames := []string{"influx", "kubelet"}
	if names, ok := args["--source"].([]string); ok && len(names) > 0 {
		metricsSourcesNames = names
		failOnError = true
	}

	kubeletClient, err := NewKubeletClient(client.Logger, scanner, kube, args)
	if err != nil {
		foundErrors = append(foundErrors, err)
		failOnError = true
	}

	for _, metricsSource := range metricsSourcesNames {
		switch metricsSource {
		case "kubelet":
			client.Info("using kubelet as metrics source")

			kubelet, err := NewKubelet(
				kubeletClient,
				client.Logger,
				metricsInterval,
				kubeletTimeouts{
					backoff: backOff{
						sleep:      utils.MustParseDuration(args, "--kubelet-backoff-sleep"),
						maxRetries: utils.MustParseInt(args, "--kubelet-backoff-max-retries"),
					},
				},
				optInAnalysisData,
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

	cAdvisor, err := NewCAdvisor(
		kubeletClient,
		client.Logger,
		scanner,
		utils.Backoff{
			Sleep:      utils.MustParseDuration(args, "--kubelet-backoff-sleep"),
			MaxRetries: utils.MustParseInt(args, "--kubelet-backoff-max-retries"),
		},
	)

	if err != nil {
		foundErrors = append(foundErrors, karma.Format(
			err,
			"unable to initialize cAdvisor source",
		))
	}

	if len(foundErrors) > 0 && (failOnError || len(metricsSources) == 0) {
		return nil, karma.Format(foundErrors, "unable to init metric sources")
	}

	for _, source := range metricsSources {
		go watchMetrics(
			client,
			source,
			scanner,
			metricsInterval,
		)
	}

	go watchMetrics2(
		client,
		cAdvisor,
		metricsInterval,
	)

	return metricsSources, nil
}
