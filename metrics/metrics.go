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

func watchMetricsProm(
	c *client.Client,
	source Source,
	interval time.Duration,
) {
	ticker := utils.NewTicker("raw-metrics", interval, func() {
		metricsBatch, err := source.GetMetrics()
		if err != nil {
			c.Errorf(err, "unable to retrieve metricsBatch from sink")
			return
		}

		packet := packetMetricsProm(metricsBatch)

		c.Pipe(client.Package{
			Kind:        proto.PacketKindMetricsPromStoreRequest,
			ExpiryTime:  utils.After(2 * time.Hour),
			ExpiryCount: 100,
			Priority:    4,
			Retries:     10,
			Data:        packet,
		})
	})
	ticker.Start(false, true, true)
}

func packetMetricsProm(metricsBatch *MetricsBatch) *proto.PacketMetricsPromStoreRequest {
	packet := &proto.PacketMetricsPromStoreRequest{
		Timestamp: metricsBatch.Timestamp,
		Metrics:   make([]*proto.PacketMetricFamilyItem, len(metricsBatch.Metrics)),
	}

	for i, metricFamily := range metricsBatch.Metrics {
		familyItem := &proto.PacketMetricFamilyItem{
			Name:   metricFamily.Name,
			Type:   metricFamily.Type,
			Help:   metricFamily.Help,
			Tags:   metricFamily.Tags,
			Values: make([]*proto.PacketMetricValueItem, len(metricFamily.Values)),
		}
		for j, metricValue := range metricFamily.Values {
			familyItem.Values[j] = &proto.PacketMetricValueItem{
				Node:        metricValue.Node,
				Application: metricValue.Application,
				Service:     metricValue.Service,
				Container:   metricValue.Container,

				Tags:  metricValue.Tags,
				Value: metricValue.Value,
			}
		}

		packet.Metrics[i] = familyItem
	}

	return packet
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
) error {
	var (
		metricsInterval = utils.MustParseDuration(args, "--metrics-interval")
		failOnError     = false // whether the agent will fail to start if an error happened during init metric source

		metricsSources = make([]interface{}, 0)
		foundErrors    = make([]error, 0)
	)

	metricsSourcesNames := []string{"alpha-cadvisor", "kubelet"}
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


		case "alpha-cadvisor":
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
				continue
			}

			metricsSources = append(metricsSources, cAdvisor)
		}
	}

	if len(foundErrors) > 0 && (failOnError || len(metricsSources) == 0) {
		return karma.Format(foundErrors, "unable to init metric sources")
	}

	for _, source := range metricsSources {
		switch s := source.(type) {
		case MetricsSource:
			go watchMetrics(
				client,
				s,
				scanner,
				metricsInterval,
			)
			break
		case Source:
			go watchMetricsProm(
				client,
				s,
				metricsInterval,
			)
			break
		}
	}

	return nil
}
