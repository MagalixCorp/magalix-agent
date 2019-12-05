package metrics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MagalixTechnologies/alltogether-go"
	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
	corev1 "k8s.io/api/core/v1"
)

type KubeletSummaryContainer struct {
	Name      string
	StartTime time.Time

	CPU struct {
		Time                 time.Time
		UsageCoreNanoSeconds int64
	}

	Memory struct {
		Time     time.Time
		RSSBytes int64
	}

	RootFS struct {
		Time      time.Time
		UsedBytes int64
	}
}

// KubeletSummary a struct to hold kubelet summary
type KubeletSummary struct {
	Node struct {
		CPU struct {
			Time                 time.Time
			UsageCoreNanoSeconds int64
		}

		Memory struct {
			Time     time.Time
			RSSBytes int64
		}

		FS struct {
			Time          time.Time
			UsedBytes     int64
			CapacityBytes int64
		}

		Network struct {
			Time     time.Time
			RxBytes  int64
			RxErrors int64
			TxBytes  int64
			TxErrors int64
		}
	}
	Pods []struct {
		PodRef struct {
			Name      string
			Namespace string
		}

		Containers []KubeletSummaryContainer
		Network    struct {
			Time     time.Time
			RxBytes  int64
			RxErrors int64
			TxBytes  int64
			TxErrors int64
		}
	}
}

// KubeletValue timestamp value struct
type KubeletValue struct {
	Timestamp time.Time
	Value     int64
}

type backOff struct {
	sleep      time.Duration
	maxRetries int
}

type kubeletTimeouts struct {
	backoff backOff
}

// Kubelet kubelet client
type Kubelet struct {
	*log.Logger

	resolution    time.Duration
	previous      map[string]KubeletValue
	previousMutex *sync.Mutex
	timeouts      kubeletTimeouts
	kubeletClient *KubeletClient

	optInAnalysisData bool
}

// NewKubelet returns new kubelet
func NewKubelet(
	kubeletClient *KubeletClient,
	log *log.Logger,
	resolution time.Duration,
	timeouts kubeletTimeouts,
	optInAnalysisData bool,
) (*Kubelet, error) {
	kubelet := &Kubelet{
		Logger: log,

		kubeletClient: kubeletClient,

		resolution:    resolution,
		previous:      map[string]KubeletValue{},
		previousMutex: &sync.Mutex{},
		timeouts:      timeouts,

		optInAnalysisData: optInAnalysisData,
	}

	return kubelet, nil
}

// GetMetrics gets metrics
func (kubelet *Kubelet) GetMetrics(
	entitiesProvider EntitiesProvider, tickTime time.Time,
) ([]*Metric, map[string]interface{}, error) {
	kubelet.collectGarbage()

	metricsMutex := &sync.Mutex{}
	metrics := []*Metric{}

	rawMutex := &sync.Mutex{}
	rawResponses := map[string]interface{}{}

	getKey := func(
		measurement string,
		namespaceName string,
		entityKind string,
		entityName string,
		containerName string,
	) string {
		key := fmt.Sprintf(
			"%s:%s/%s/%s",
			measurement,
			namespaceName,
			entityKind,
			entityName,
		)

		if containerName != "" {
			key = fmt.Sprintf("%s/%s", key, containerName)
		}

		return key
	}

	calcRate := func(
		key string,
		timestamp time.Time,
		value int64,
		multiplier int64,
	) (int64, error) {

		previous, err := kubelet.getPreviousValue(key)

		if err != nil {
			return 0, err
		}

		duration := timestamp.UnixNano() - previous.Timestamp.UnixNano()

		if duration <= time.Second.Nanoseconds() {
			return 0, karma.Format(nil, "timestamp less than or equal previous one")
		}

		previousValue := previous.Value
		if previousValue > value {
			// we have a restart for this entity so the cumulative
			// value is reset so we should reset as well
			previousValue = 0
		}
		rate := multiplier * (value - previousValue) / duration

		return rate, nil
	}

	addMetric := func(metric *Metric) {
		metricsMutex.Lock()
		defer metricsMutex.Unlock()
		if metric.Timestamp.Equal(time.Time{}) {
			kubelet.Errorf(
				karma.Describe("metric", metric.Name).
					Describe("type", metric.Type).
					Describe("timestamp", metric.Timestamp).
					Reason(fmt.Errorf("invalid timestamp")),
				"invalid timestamp detect. defaulting to tickTime",
			)
			metric.Timestamp = tickTime
		}
		metrics = append(metrics, metric)
	}
	addMetricValue := func(
		measurementType string,
		measurement string,
		nodeName string,
		nodeIP string,
		namespaceName string,
		controllerName string,
		controllerKind string,
		containerName string,
		podName string,
		timestamp time.Time,
		value int64,
	) {
		addMetric(&Metric{
			Name:           measurement,
			Type:           measurementType,
			NodeName:       nodeName,
			NamespaceName:  namespaceName,
			ControllerName: controllerName,
			ControllerKind: controllerKind,
			ContainerName:  containerName,
			PodName:        podName,
			Timestamp:      timestamp,
			Value:          value,
		})
	}
	addMetricValueWithTags := func(
		measurementType string,
		measurement string,
		nodeName string,
		nodeIP string,
		namespaceName string,
		controllerName string,
		controllerKind string,
		containerName string,
		podName string,
		timestamp time.Time,
		value int64,
		additionalTags map[string]interface{},
	) {
		metricsMutex.Lock()
		defer metricsMutex.Unlock()
		metrics = append(metrics, &Metric{
			Name:           measurement,
			Type:           measurementType,
			NodeName:       nodeName,
			NamespaceName:  namespaceName,
			ControllerName: controllerName,
			ControllerKind: controllerKind,
			ContainerName:  containerName,
			PodName:        podName,
			Timestamp:      timestamp,
			Value:          value,
			AdditionalTags: additionalTags,
		})
	}

	addMetricRate := func(
		entityKind string,
		entityName string,
		multiplier int64,
		metric *Metric,
	) {
		if metric.Timestamp.Equal(time.Time{}) {
			kubelet.Errorf(
				karma.Describe("metric", metric.Name).
					Describe("type", metric.Type).
					Describe("timestamp", metric.Timestamp).
					Reason(fmt.Errorf("invalid timestamp")),
				"{rate} invalid timestamp detect. defaulting to tickTime",
			)
			metric.Timestamp = tickTime
		}

		key := getKey(metric.Name, metric.NamespaceName, entityKind, entityName, metric.ContainerName)
		rate, err := calcRate(key, metric.Timestamp, metric.Value, multiplier)
		kubelet.updatePreviousValue(key, &KubeletValue{
			Timestamp: metric.Timestamp,
			Value:     metric.Value,
		})

		if err != nil {
			kubelet.Warningf(
				karma.Describe("metric", metric.Name).
					Describe("type", metric.Type).
					Describe("timestamp", metric.Timestamp).
					Reason(err),
				"can't calculate rate",
			)
			return
		}
		metric.Value = rate
		addMetric(metric)
	}
	addMetricValueRate := func(
		measurementType string,
		entityKind string,
		entityName string,
		measurement string,
		nodeName string,
		nodeIP string,
		namespaceName string,
		controllerName string,
		controllerKind string,
		containerName string,
		podName string,
		timestamp time.Time,
		value int64,
		multiplier int64,
	) {
		addMetricRate(
			entityKind,
			entityName,
			multiplier,
			&Metric{
				Name:           measurement,
				Type:           measurementType,
				NodeName:       nodeName,
				NodeIP:			nodeIP,
				NamespaceName:  namespaceName,
				ControllerName: controllerName,
				ControllerKind: controllerKind,
				ContainerName:  containerName,
				PodName:        podName,
				Timestamp:      timestamp,
				Value:          value,
			},
		)
	}

	addRawResponse := func(nodeName string, data interface{}) {
		rawMutex.Lock()
		defer rawMutex.Unlock()
		rawResponses[nodeName] = data
	}

	// scanner scans the nodes every 1m, so assume latest value is up to date
	nodes, err := entitiesProvider.GetNodes()
	if err != nil {
		return nil, nil, karma.Format(err, "Can't get nodes")
	}

	addMetricValue(
		TypeCluster,
		"nodes/count",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		tickTime,
		int64(len(nodes)),
	)

	instanceGroups := map[string]int64{}
	for _, node := range nodes {
		instanceGroup := GetNodeInstanceGroup(node)
		if _, ok := instanceGroups[instanceGroup]; !ok {
			instanceGroups[instanceGroup] = 0
		}

		instanceGroups[instanceGroup] = instanceGroups[instanceGroup] + 1
	}

	for instanceGroup, nodesCount := range instanceGroups {
		addMetricValueWithTags(
			TypeCluster,
			"nodes/count",
			"",
			"",
			"",
			"",
			"",
			"",
			"",
			tickTime,
			nodesCount,
			map[string]interface{}{
				"instance_group": instanceGroup,
			},
		)
	}

	for _, node := range nodes {
		for _, measurement := range []struct {
			Name  string
			Time  time.Time
			Value int64
		}{
			{"cpu/node_capacity", tickTime, node.Status.Capacity.Cpu().MilliValue()},
			{"cpu/node_allocatable", tickTime, node.Status.Allocatable.Cpu().MilliValue()},
			{"memory/node_capacity", tickTime, node.Status.Capacity.Memory().Value()},
			{"memory/node_allocatable", tickTime, node.Status.Allocatable.Memory().Value()},
		} {
			addMetricValue(
				TypeNode,
				measurement.Name,
				node.Name,
				GetNodeIP(&node),
				"",
				"",
				"",
				"",
				"",
				measurement.Time,
				measurement.Value,
			)
		}
	}

	pods, err := entitiesProvider.GetPods()
	if err != nil {
		return nil, nil, karma.Format(err, "unable to get pods")
	}

	for _, pod := range pods {
		controllerName, controllerKind, err := entitiesProvider.FindController(pod.Namespace, pod.Name)
		if err != nil {
			kubelet.Logger.Errorf(
				karma.Describe("pod_name", pod.Name).
					Describe("namespace", pod.Namespace).
					Reason(err),
				"unable to find pod controller",
			)
		}

		for _, container := range pod.Spec.Containers {
			for _, measurement := range []struct {
				Name  string
				Value int64
			}{
				{"cpu/request", container.Resources.Requests.Cpu().MilliValue()},
				{"cpu/limit", container.Resources.Limits.Cpu().MilliValue()},

				{"memory/request", container.Resources.Requests.Memory().Value()},
				{"memory/limit", container.Resources.Limits.Memory().Value()},
			} {
				addMetricValue(
					TypePodContainer,
					measurement.Name,
					pod.Spec.NodeName,
					pod.Status.HostIP,
					pod.Namespace,
					controllerName,
					controllerKind,
					container.Name,
					pod.Name,
					tickTime,
					measurement.Value,
				)
			}
		}
	}

	pr, err := alltogether.NewConcurrentProcessor(
		nodes,
		func(node corev1.Node) error {
			kubelet.Infof(
				nil,
				"{kubelet} requesting metrics from node %s",
				node.Name,
			)

			var (
				cadvisorResponse []byte
				summaryBytes     []byte
				summary          KubeletSummary
			)
			err := kubelet.withBackoff(func() error {
				var err error
				summaryBytes, err = kubelet.kubeletClient.GetBytes(&node, "stats/summary")
				if err != nil {
					if strings.Contains(err.Error(), "the server could not find the requested resource") {
						kubelet.Warningf(err, "unable to get summary from node %q", node.Name)
						summaryBytes = []byte("{}")
						return nil
					}
					return karma.Format(
						err,
						"{kubelet} unable to get summary from node %q",
						node.Name,
					)
				}
				return nil
			})

			if err != nil {
				return err
			}

			if kubelet.optInAnalysisData {
				var summaryInterface interface{}
				err = json.Unmarshal(summaryBytes, &summaryInterface)
				if err != nil {
					kubelet.Errorf(
						err,
						"{kubelet} unable to unmarshal summary response to its raw interface",
					)
				}
				if summaryInterface != nil {
					addRawResponse(node.Name, &summaryInterface)
				}
			}

			err = json.Unmarshal(summaryBytes, &summary)
			if err != nil {
				return karma.Format(
					err,
					"{kubelet} unable to unmarshal summary response",
				)
			}

			for _, measurement := range []struct {
				Name  string
				Time  time.Time
				Value int64
			}{
				{"cpu/usage", summary.Node.CPU.Time, summary.Node.CPU.UsageCoreNanoSeconds},
				{"memory/rss", summary.Node.Memory.Time, summary.Node.Memory.RSSBytes},
				{"filesystem/usage", summary.Node.FS.Time, summary.Node.FS.UsedBytes},
				{"filesystem/node_capacity", summary.Node.FS.Time, summary.Node.FS.CapacityBytes},
				{"filesystem/node_allocatable", summary.Node.FS.Time, summary.Node.FS.CapacityBytes},
				{"network/tx", summary.Node.Network.Time, summary.Node.Network.TxBytes},
				{"network/rx", summary.Node.Network.Time, summary.Node.Network.RxBytes},
				{"network/tx_errors", summary.Node.Network.Time, summary.Node.Network.TxErrors},
				{"network/rx_errors", summary.Node.Network.Time, summary.Node.Network.RxErrors},
			} {
				addMetricValue(
					TypeNode,
					measurement.Name,
					node.Name,
					GetNodeIP(&node),
					"",
					"",
					"",
					"",
					"",
					measurement.Time,
					measurement.Value,
				)
			}

			for _, measurement := range []struct {
				Name       string
				Time       time.Time
				Value      int64
				Multiplier int64
			}{
				{"cpu/usage_rate", summary.Node.CPU.Time, summary.Node.CPU.UsageCoreNanoSeconds, 1000},
				{"network/tx_rate", summary.Node.Network.Time, summary.Node.Network.TxBytes, 1e9},
				{"network/rx_rate", summary.Node.Network.Time, summary.Node.Network.RxBytes, 1e9},
				{"network/tx_errors_rate", summary.Node.Network.Time, summary.Node.Network.TxErrors, 1e9},
				{"network/rx_errors_rate", summary.Node.Network.Time, summary.Node.Network.RxErrors, 1e9},
			} {

				addMetricValueRate(
					TypeNode,
					node.Kind,
					node.Name,
					measurement.Name,
					node.Name,
					GetNodeIP(&node),
					"",
					"",
					"",
					"",
					"",
					measurement.Time,
					measurement.Value,
					measurement.Multiplier,
				)
			}

			throttleMetrics := map[string]*Metric{}

			for _, pod := range summary.Pods {
				controllerName, controllerKind, err := entitiesProvider.FindController(
					pod.PodRef.Namespace, pod.PodRef.Name,
				)
				namespaceName := pod.PodRef.Namespace

				if err != nil {
					kubelet.Logger.Warningf(
						karma.Describe("namespace", pod.PodRef.Namespace).
							Describe("pod_name", pod.PodRef.Name).
							Reason(err),
						"unable to find controller for pod",
					)
					continue
				}

				for _, measurement := range []struct {
					Name  string
					Time  time.Time
					Value int64
				}{
					{"network/tx", pod.Network.Time, pod.Network.TxBytes},
					{"network/rx", pod.Network.Time, pod.Network.TxBytes},
					{"network/tx_errors", pod.Network.Time, pod.Network.TxErrors},
					{"network/rx_errors", pod.Network.Time, pod.Network.RxErrors},
				} {
					addMetricValue(
						TypePod,
						measurement.Name,
						node.Name,
						GetNodeIP(&node),
						namespaceName,
						controllerName,
						controllerKind,
						"",
						pod.PodRef.Name,
						measurement.Time,
						measurement.Value,
					)
				}

				for _, measurement := range []struct {
					Name  string
					Time  time.Time
					Value int64
				}{
					{"network/tx_rate", pod.Network.Time, pod.Network.TxBytes},
					{"network/rx_rate", pod.Network.Time, pod.Network.TxBytes},
					{"network/tx_errors_rate", pod.Network.Time, pod.Network.TxErrors},
					{"network/rx_errors_rate", pod.Network.Time, pod.Network.RxErrors},
				} {
					addMetricValueRate(
						TypePod,
						"Pod",
						pod.PodRef.Name,
						measurement.Name,
						node.Name,
						GetNodeIP(&node),
						namespaceName,
						controllerName,
						controllerKind,
						"",
						pod.PodRef.Name,
						measurement.Time,
						measurement.Value,
						1e9,
					)
				}

				// NOTE: possible bug in cAdvisor
				// Sometimes, when a container is restarted cAdvisor don't
				// understand this. It don't delete old stats of the old deleted
				// container but creates new stats for the new one.
				// Hence, we get two stats for two containers with the same name
				// and this lead to expected behavior.
				// This workaround filter containers with the same name in the
				// the same pod and take only the newer started one.
				podContainers := map[string]KubeletSummaryContainer{}
				for _, container := range pod.Containers {
					if foundContainer, ok := podContainers[container.Name]; !ok {
						// add to unique containers
						podContainers[container.Name] = container
					} else {
						if container.StartTime.After(foundContainer.StartTime) {
							// override the old container with the new started
							// one
							podContainers[container.Name] = container
						}
					}
				}

				for _, container := range podContainers {
					for _, measurement := range []struct {
						Name  string
						Time  time.Time
						Value int64
					}{
						{"cpu/usage", container.CPU.Time, container.CPU.UsageCoreNanoSeconds},
						{"memory/rss", container.Memory.Time, container.Memory.RSSBytes},
						{"filesystem/usage", container.RootFS.Time, container.RootFS.UsedBytes},
					} {
						addMetricValue(
							TypePodContainer,
							measurement.Name,
							node.Name,
							GetNodeIP(&node),
							namespaceName,
							controllerName,
							controllerKind,
							container.Name,
							pod.PodRef.Name,
							measurement.Time,
							measurement.Value,
						)
					}

					addMetricValueRate(
						TypePodContainer,
						controllerKind,
						controllerName,
						"cpu/usage_rate",
						node.Name,
						GetNodeIP(&node),
						namespaceName,
						controllerName,
						controllerKind,
						container.Name,
						pod.PodRef.Name,
						container.CPU.Time,
						container.CPU.UsageCoreNanoSeconds,
						1000, // cpu_rate is in millicore
					)

					// Set default zero values for throttled metrics
					periodsKey := getKey(
						"container_cpu_cfs/periods_total",
						namespaceName,
						controllerKind,
						controllerName,
						container.Name,
					)
					throttleMetrics[periodsKey] = &Metric{
						Name: "container_cpu_cfs/periods_total",
						Type: TypePodContainer,

						NamespaceName:  namespaceName,
						ControllerName: controllerName,
						ControllerKind: controllerKind,
						ContainerName:  container.Name,
						PodName:        pod.PodRef.Name,
						Timestamp:      container.CPU.Time,
						Value:          0,
					}
					throttledSecondsKey := getKey(
						"container_cpu_cfs_throttled/seconds_total",
						namespaceName,
						controllerKind,
						controllerName,
						container.Name,
					)
					throttleMetrics[throttledSecondsKey] = &Metric{
						Name: "container_cpu_cfs_throttled/seconds_total",
						Type: TypePodContainer,

						NamespaceName:  namespaceName,
						ControllerName: controllerName,
						ControllerKind: controllerKind,
						ContainerName:  container.Name,
						PodName:        pod.PodRef.Name,
						Timestamp:      container.CPU.Time,
						Value:          0,
					}
					throttledPeriodsKey := getKey(
						"container_cpu_cfs_throttled/periods_total",
						namespaceName,
						controllerKind,
						controllerName,
						container.Name,
					)
					throttleMetrics[throttledPeriodsKey] = &Metric{
						Name: "container_cpu_cfs_throttled/periods_total",
						Type: TypePodContainer,

						NamespaceName:  namespaceName,
						ControllerName: controllerName,
						ControllerKind: controllerKind,
						ContainerName:  container.Name,
						PodName:        pod.PodRef.Name,
						Timestamp:      container.CPU.Time,
						Value:          0,
					}
				}
			}

			err = kubelet.withBackoff(func() error {
				cadvisorResponse, err = kubelet.kubeletClient.GetBytes(
					&node,
					"metrics/cadvisor",
				)
				if err != nil {
					if strings.Contains(err.Error(), "the server could not find the requested resource") {
						kubelet.Warningf(err,
							"{cAdvisor} unable to get cAdvisor from node %q",
							node.Name,
						)
						cadvisorResponse = []byte{}
						return nil
					}
					return karma.Format(
						err,
						"{kubelet} unable to get cadvisor from node %q",
						node.Name,
					)
				}
				return nil
			})

			if err != nil {
				return err
			}

			cadvisor, err := decodeCAdvisorResponse(bytes.NewReader(cadvisorResponse))
			if err != nil {
				return karma.Format(err,
					"{kubelet} unable to read cadvisor response",
				)
			}

			for _, metric := range []struct {
				Name string
				Ref  string
			}{
				{"container_cpu_cfs/periods_total", "container_cpu_cfs_periods_total"},
				{"container_cpu_cfs_throttled/periods_total", "container_cpu_cfs_throttled_periods_total"},
				{"container_cpu_cfs_throttled/seconds_total", "container_cpu_cfs_throttled_seconds_total"},
			} {
				for _, val := range cadvisor[metric.Ref] {
					namespaceName, podName, containerName, value, ok := getCAdvisorContainerValue(val)
					ctx :=
						karma.
							Describe("namespace", namespaceName).
							Describe("pod_name", podName).
							Describe("container_name", containerName)
					if ok {
						controllerName, controllerKind, err := entitiesProvider.FindController(namespaceName, podName)
						if err != nil {
							kubelet.Logger.Errorf(
								ctx.Reason(err),
								"unable to find controller for pod",
							)
						}
						key := getKey(
							metric.Name,
							namespaceName,
							controllerKind,
							controllerName,
							containerName,
						)
						if storedMetric, ok := throttleMetrics[key]; ok {
							storedMetric.Value = int64(value)
						} else {
							kubelet.Warningf(
								ctx.Reason(nil),
								"found a container in cAdvisor response that don't exist at summary response",
							)
						}
					}
				}
			}

			for _, metric := range throttleMetrics {
				addMetric(metric)

				rateMetric := *metric
				rateMetric.Name += "_rate"

				// TODO: cleanup when values are sent as floats
				// covert seconds to milliseconds
				if strings.Contains(rateMetric.Name, "seconds") {
					rateMetric.Value *= 1000
				}

				// Container metrics use controller name & kind as entity name & kind
				addMetricRate(
					rateMetric.ControllerKind,
					rateMetric.ControllerName,
					1e9,
					&rateMetric,
				)
			}

			return nil
		},
	)
	if err != nil {
		panic(err)
	}

	// Start concurrent getter of details:
	errs := pr.Do()
	if !errs.AllNil() {
		// Note: if one node fails we fail safe to allow other node metrics to flow.
		// Note: In cases where pods are replicated across nodes,
		// Note: it means that the metrics are misleading. However, It is the
		// Note: rule of resampler to validate the correctness of the metrics
		// Note: and drop bad points

		for _, err := range errs {
			if err != nil {
				kubelet.Errorf(
					karma.Format(err, "error while scraping node metrics"),
					"error while scraping nodes metrics",
				)
			}
		}
	}

	result := []*Metric{}

	var context *karma.Context
	for _, metrics := range metrics {

		/*
			context = context.Describe(
				fmt.Sprintf(
					"%s %s %s %s",
					metrics.Node,
					metrics.Application,
					metrics.Service,
					metrics.Container,
				),
				metrics.Name,
			)
		*/

		result = append(result, metrics)
	}

	if len(metrics) > 0 {
		kubelet.Infof(
			context,
			"{kubelet} collected %d measurements with timestamp %s",
			len(metrics),
			metrics[0].Timestamp,
		)
	} else {
		kubelet.Infof(
			context,
			"{kubelet} collected %d measurements",
			len(metrics),
		)
	}

	if !kubelet.optInAnalysisData {
		rawResponses = nil
	}

	return result, rawResponses, nil
}

func (kubelet *Kubelet) collectGarbage() {
	for key, previous := range kubelet.previous {
		if time.Now().Sub(previous.Timestamp) > time.Hour {
			delete(kubelet.previous, key)
		}
	}
}

func (kubelet *Kubelet) getPreviousValue(key string) (*KubeletValue, error) {
	kubelet.previousMutex.Lock()
	defer kubelet.previousMutex.Unlock()

	previous, ok := kubelet.previous[key]

	if !ok {
		return nil, karma.Format(nil, "No previous value")
	}

	// make new copy
	return &KubeletValue{
		Value:     previous.Value,
		Timestamp: previous.Timestamp,
	}, nil
}
func (kubelet *Kubelet) updatePreviousValue(key string, value *KubeletValue) {
	kubelet.previousMutex.Lock()
	defer kubelet.previousMutex.Unlock()

	kubelet.previous[key] = *value
}

func (kubelet *Kubelet) withBackoff(fn func() error) error {
	maxRetry := kubelet.timeouts.backoff.maxRetries
	try := 0
	for {
		try++

		err := fn()
		if err == nil {
			return nil
		}

		if try > maxRetry {
			context := karma.
				Describe("retry", try).
				Describe("maxRetry", maxRetry).
				Reason(err)
			kubelet.Errorf(
				context,
				"unhandled error occurred, no more retrying",
			)

			return karma.Format(context, "max retries exceeded")
		}

		// NOTE max multiplier = 10
		// 300ms -> 600ms -> [...] -> 3000ms -> 300ms
		timeout := kubelet.timeouts.backoff.sleep * time.Duration((try-1)%10+1)

		kubelet.Warningf(
			karma.Describe("retry", try).Reason(err),
			"unhandled error occurred, retrying after %s",
			timeout,
		)

		time.Sleep(timeout)
	}
}

func GetNodeInstanceGroup(node corev1.Node) string {
	labels := node.Labels
	instanceType, cloudProvider := labels["beta.kubernetes.io/instance-type"]
	instanceSize := ""

	if cloudProvider {
		_, gcloud := labels["cloud.google.com/gke-nodepool"]
		if gcloud {
			if strings.Contains(instanceType, "-") {
				parts := strings.SplitN(instanceType, "-", 2)
				instanceType, instanceSize = parts[0], parts[1]
			}
		} else {
			if strings.Contains(instanceType, ".") {
				parts := strings.SplitN(instanceType, ".", 2)
				instanceType, instanceSize = parts[0], parts[1]
			}
		}
	} else {
		// for custom on-perm clusters we use node capacity as instance type
		instanceType = "custom"

		cpuCores := node.Status.Capacity.Cpu().MilliValue() / 1000
		memoryGi := node.Status.Capacity.Memory().Value() / 1024 / 1024 / 1024

		instanceSize = fmt.Sprintf(
			"cpu-%d--memory-%.2f",
			cpuCores,
			memoryGi,
		)
	}

	instanceGroup := ""
	if instanceType != "" {
		instanceGroup = instanceType
	}
	if instanceSize != "" {
		instanceGroup += "." + instanceSize
	}

	return instanceGroup
}
