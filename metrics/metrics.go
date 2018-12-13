package metrics

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MagalixTechnologies/agent/client"
	"github.com/MagalixTechnologies/agent/kuber"
	"github.com/MagalixTechnologies/agent/proto"
	"github.com/MagalixTechnologies/agent/scanner"
	"github.com/MagalixTechnologies/agent/utils"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
)

const limit = 1000

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

func nextTick(interval time.Duration) <-chan time.Time {
	if time.Hour%interval == 0 {
		now := time.Now()
		// TODO: sub seconds
		nanos := time.Second*time.Duration(now.Second()) + time.Minute*time.Duration(now.Minute())
		next := interval - nanos%interval
		return time.After(next)
	}
	return time.After(interval)
}

func watchMetrics(
	client *client.Client,
	source MetricsSource,
	scanner *scanner.Scanner,
	interval time.Duration,
) {
	pipe := make(chan []*Metrics)
	go sendMetrics(client, pipe)
	defer close(pipe)

	ticker := nextTick(interval)
	for {
		<-ticker
		metrics, err := source.GetMetrics(scanner)
		if err != nil {
			client.Errorf(err, "unable to retrieve metrics from sink")
		}

		for i := 0; i < len(metrics); i += limit {
			pipe <- metrics[i:min(i+limit, len(metrics))]
		}
		ticker = nextTick(interval)
	}
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
func sendMetricsBatch(client *client.Client, metrics []*Metrics) {
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
	client.WithBackoff(func() error {
		var response proto.PacketMetricsStoreResponse
		return client.Send(proto.PacketKindMetricsStoreRequest, req, &response)
	})
}

func InitMetrics(
	client *client.Client,
	scanner *scanner.Scanner,
	args map[string]interface{},
) ([]MetricsSource, error) {
	var (
		metricsInterval = utils.MustParseDuration(args, "--metrics-interval")
		failOnError     = false // whether the agent will fail to start if an error happened during init metric source

		kubeletAddress, _ = args["--kubelet-address"].(string)
		kubeletPort, _    = args["--kubelet-port"].(string)

		metricsSources = make([]MetricsSource, 0)
		foundErrors    = make([]error, 0)
	)

	metricsSourcesNames := []string{"influx", "kubelet"}
	if names, ok := args["--source"].([]string); ok && len(names) > 0 {
		metricsSourcesNames = names
		failOnError = true
	}

	for _, metricsSource := range metricsSourcesNames {
		switch metricsSource {
		case "kubelet":
			client.Info("using kubelet as metrics source")

			if kubeletAddress != "" && strings.HasPrefix(kubeletAddress, "/") {
				foundErrors = append(foundErrors, errors.New(
					"kubelet address should not start with /",
				))
				continue
			}

			var getNodeKubeletAddress func(node kuber.Node) string
			if kubeletAddress != "" {
				getNodeKubeletAddress = func(node kuber.Node) string {
					return kubeletAddress
				}
			} else {
				getNodeKubeletAddress = func(node kuber.Node) string {
					return fmt.Sprintf("%s:%s", node.IP, kubeletPort)
				}
			}
			kubelet, err := NewKubelet(getNodeKubeletAddress, client.Logger, metricsInterval,
				kubeletTimeouts{
					backoff: backOff{
						sleep:      utils.MustParseDuration(args, "--kubelet-backoff-sleep"),
						maxRetries: utils.MustParseInt(args, "--kubelet-backoff-max-retries"),
					},
				})
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

	return metricsSources, nil
}
