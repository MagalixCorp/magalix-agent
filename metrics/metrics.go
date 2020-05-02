package metrics

import (
	"github.com/MagalixCorp/magalix-agent/v2/scanner"
	"github.com/MagalixTechnologies/log-go"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/client"
	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
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
	useMetricsPacketV2 bool,
	interval time.Duration,
) {
	metricsPipe := make(chan []*Metric)
	go sendMetrics(client, useMetricsPacketV2, metricsPipe)
	defer close(metricsPipe)

	ticker := utils.NewTicker("metrics", interval, func(tickTime time.Time) {
		metrics, raw, err := source.GetMetrics(entitiesProvider, tickTime)

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

func packetMetricsProm(metricsBatch *MetricsBatch) *proto.PacketMetricsPromStoreRequest {
	packet := &proto.PacketMetricsPromStoreRequest{
		Timestamp: metricsBatch.Timestamp,
		Metrics:   make([]*proto.PacketMetricFamilyItem, len(metricsBatch.Metrics)),
	}

	i := 0
	for _, metricFamily := range metricsBatch.Metrics {
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
		i++
	}

	return packet
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func sendMetrics(client *client.Client, useMetricsPacketV2 bool, pipe chan []*Metric) {
	queueLimit := 100
	queue := make(chan []*Metric, queueLimit)
	defer close(queue)
	go func() {
		for metrics := range queue {
			if len(metrics) > 0 {
				client.Infof(karma.Describe("timestamp", metrics[0].Timestamp), "sending metrics")
				sendMetricsBatch(client, useMetricsPacketV2, metrics)
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

var logger = log.New(true, true, "agent.log")

// SendMetrics bulk send metrics
func sendMetricsBatch(c *client.Client, useMetricsPacketV2 bool, metrics []*Metric) {
	ctx := karma.
		Describe("account_id", c.AccountID).
		Describe("cluster_id", c.ClusterID)

	var packet interface{}
	var packetKind proto.PacketKind
	if useMetricsPacketV2 {
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
	} else {
		var req proto.PacketMetricsStoreRequest
		for _, metric := range metrics {
			ctx.Describe("metric_type", metric.Type)

			var nodeID, namespaceID, controllerID, containerID uuid.UUID
			var err error

			if metric.NodeName != "" {
				nodeID, err = scanner.IdentifyEntity(metric.NodeIP, c.ClusterID)
				if err != nil {
					c.Logger.Errorf(
						ctx.Describe("node_name", metric.NodeName).Reason(err),
						"unable to generate node id",
					)
					continue
				}
			}

			if metric.NamespaceName != "" {
				namespaceID, err = scanner.IdentifyEntity(metric.NamespaceName, c.ClusterID)
				if err != nil {
					c.Logger.Errorf(
						ctx.Describe("namespace_name", metric.NamespaceName).Reason(err),
						"unable to generate namespace id",
					)
					continue
				}
			}

			if metric.ControllerName != "" {
				controllerID, err = scanner.IdentifyEntity(metric.ControllerName, namespaceID)
				if err != nil {
					c.Logger.Errorf(
						ctx.Describe("namespace_name", metric.NamespaceName).
							Describe("controller_name", metric.ControllerName).
							Reason(err),
						"unable to generate controller id",
					)
					continue
				}
			}

			if metric.ContainerName != "" {
				containerID, err = scanner.IdentifyEntity(metric.ContainerName, controllerID)
				if err != nil {
					c.Logger.Errorf(
						ctx.Describe("namespace_name", metric.NamespaceName).
							Describe("controller_name", metric.ControllerName).
							Describe("container_name", metric.ContainerName).
							Reason(err),
						"unable to generate controller id",
					)
					continue
				}
			}
			req = append(req, proto.MetricStoreRequest{
				Name:        metric.Name,
				Type:        metric.Type,
				Node:        nodeID,
				Application: namespaceID,
				Service:     controllerID,
				Container:   containerID,
				Timestamp:   metric.Timestamp,
				Value:       metric.Value,
				Pod:         metric.PodName,

				AdditionalTags: metric.AdditionalTags,
			})
		}
		packet = req
		packetKind = proto.PacketKindMetricsStoreRequest
	}
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
	identifyEntities bool,
	optInAnalysisData bool,
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

	kubeletClient, err := NewKubeletClient(client.Logger, nodesProvider, kube, args)
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

	if len(foundErrors) > 0 && (failOnError || len(metricsSources) == 0) {
		return karma.Format(foundErrors, "unable to init metric sources")
	}

	for _, source := range metricsSources {
		go watchMetrics(
			client,
			source,
			entitiesProvider,
			identifyEntities,
			metricsInterval,
		)
	}

	return nil
}
