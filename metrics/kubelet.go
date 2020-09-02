package metrics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/MagalixTechnologies/alltogether-go"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/pkg/errors"

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
		Time            time.Time
		RSSBytes        int64
		WorkingSetBytes int64
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
	}
	Pods []struct {
		PodRef struct {
			Name      string
			Namespace string
		}

		Containers []KubeletSummaryContainer
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
	resolution time.Duration,
	timeouts kubeletTimeouts,
	optInAnalysisData bool,
) (*Kubelet, error) {
	kubelet := &Kubelet{

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
) (result []*Metric, rawResponses map[string]interface{}, err error) {
	defer func() {
		if tears := recover(); tears != nil {
			err = errors.New(string(debug.Stack()))
		}
	}()

	kubelet.collectGarbage()

	metricsMutex := &sync.Mutex{}
	metrics := make([]*Metric, 0)

	rawMutex := &sync.Mutex{}
	rawResponses = map[string]interface{}{}

	getKey := func(
		measurement string,
		namespaceName string,
		entityKind string,
		entityName string,
		podName string,
		containerName string,
	) string {
		key := fmt.Sprintf(
			"%s:%s/%s/%s",
			measurement,
			namespaceName,
			entityKind,
			entityName,
		)

		if podName != "" {
			key = fmt.Sprintf("%s/%s", key, podName)
		}
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
			return 0, errors.New("timestamp less than or equal previous one")
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
		if metric.Name == "memory/limit" || metric.Name == "memory/request" || metric.Name == "cpu/limit" || metric.Name == "cpu/request" {
			logger.Debugw("Adding metric", "metric", metric.Name, "container", metric.ContainerName)
		}

		if metric.Name == "memory/limit" || metric.Name == "memory/request" || metric.Name == "cpu/limit" || metric.Name == "cpu/request" {
			defer logger.Debugw("Finished Adding metric", "metric", metric.Name, "container", metric.ContainerName)
		}

		if metric.Timestamp.Equal(time.Time{}) {
			logger.Errorw("invalid timestamp detect. defaulting to tickTime",
				"metric", metric.Name,
				"type", metric.Type,
				"timestamp", metric.Timestamp,
			)
			metric.Timestamp = tickTime
		}

		metric.Timestamp = metric.Timestamp.Truncate(time.Minute)

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
			NodeIP:         nodeIP,
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
		addMetric(&Metric{
			Name:           measurement,
			Type:           measurementType,
			NodeName:       nodeName,
			NodeIP:         nodeIP,
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
			logger.Errorw("invalid timestamp detect. defaulting to tickTime",
				"metric", metric.Name,
				"type", metric.Type,
				"timestamp", metric.Timestamp,
			)
			metric.Timestamp = tickTime
		}

		metric.Timestamp = metric.Timestamp.Truncate(time.Minute)

		key := getKey(metric.Name, metric.NamespaceName, entityKind, entityName, metric.PodName, metric.ContainerName)
		rate, err := calcRate(key, metric.Timestamp, metric.Value, multiplier)
		kubelet.updatePreviousValue(key, &KubeletValue{
			Timestamp: metric.Timestamp,
			Value:     metric.Value,
		})

		if err != nil {
			logger.Warnw("can't calculate rate",
				"metric", metric.Name,
				"type", metric.Type,
				"timestamp", metric.Timestamp,
				"error", err,
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
				NodeIP:         nodeIP,
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

	logger.Debug("Fetching nodes")

	// scanner scans the nodes every 1m, so assume latest value is up to date
	nodes, err := entitiesProvider.GetNodes()
	if err != nil {
		return nil, nil, errors.Wrap(err, "can't get nodes")
	}

	logger.Info("===============================")
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

	logger.Info("===============================")

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

	logger.Debug("Fetching pods")

	pods, err := entitiesProvider.GetPods()
	if err != nil {
		return nil, nil, errors.New("unable to get pods")
	}

	logger.Debugf("Fetched %d pods", len(pods))
	processedPodsCount := 0
	processedContainersCount := 0

	for _, pod := range pods {
		controllerName, controllerKind, err := entitiesProvider.FindController(pod.Namespace, pod.Name)
		if err != nil {
			logger.Errorw("unable to find pod controller",
				"pod_name", pod.Name,
				"namespace", pod.Namespace,
				"error", err,
			)
		}

		processedPodsCount++
		logger.Infof("Processing %d containers in pod %s", len(pod.Spec.Containers), pod.Name)

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

		processedContainersCount += len(pod.Spec.Containers)
	}

	logger.Infof("Processed %d/%d pods and %d containers", processedPodsCount, len(pods), processedContainersCount)

	logger.Info("Fetching nodes metrics")

	pr, err := alltogether.NewConcurrentProcessor(
		nodes,
		func(node corev1.Node) error {
			nodeIP := GetNodeIP(&node)
			logger.Infof(
				"requesting metrics from node %s",
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
						logger.Warnw("unable to get summary", "node", node.Name, "error", err)
						summaryBytes = []byte("{}")
						return nil
					}
					return errors.Wrapf(
						err,
						"unable to get summary from node %q",
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
					logger.Errorw(
						"unable to unmarshal summary response to its raw interface",
						"error", err,
					)
				}
				if summaryInterface != nil {
					addRawResponse(node.Name, &summaryInterface)
				}
			}

			err = json.Unmarshal(summaryBytes, &summary)
			if err != nil {
				return errors.Wrap(
					err,
					"unable to unmarshal summary response",
				)
			}

			for _, measurement := range []struct {
				Name  string
				Time  time.Time
				Value int64
			}{
				{"cpu/usage", tickTime, summary.Node.CPU.UsageCoreNanoSeconds},
				{"memory/rss", tickTime, summary.Node.Memory.RSSBytes},
			} {
				addMetricValue(
					TypeNode,
					measurement.Name,
					node.Name,
					nodeIP,
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
				{"cpu/usage_rate", tickTime, summary.Node.CPU.UsageCoreNanoSeconds, 1000},
			} {

				addMetricValueRate(
					TypeNode,
					node.Kind,
					node.Name,
					measurement.Name,
					node.Name,
					nodeIP,
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

			throttleMetrics := make(map[string]*Metric)

			for _, pod := range summary.Pods {
				controllerName, controllerKind, err := entitiesProvider.FindController(
					pod.PodRef.Namespace, pod.PodRef.Name,
				)
				namespaceName := pod.PodRef.Namespace

				if err != nil {
					logger.Warnf(
						"unable to find controller for pod",
						"namespace", pod.PodRef.Namespace,
						"pod_name", pod.PodRef.Name,
						"error", err,
					)
					continue
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
						{"cpu/usage", tickTime, container.CPU.UsageCoreNanoSeconds},
						{"memory/rss", tickTime, int64(math.Max(float64(container.Memory.RSSBytes), float64(container.Memory.WorkingSetBytes)))},
						{"memory/working_set", tickTime, container.Memory.WorkingSetBytes},
					} {
						addMetricValue(
							TypePodContainer,
							measurement.Name,
							node.Name,
							nodeIP,
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
						nodeIP,
						namespaceName,
						controllerName,
						controllerKind,
						container.Name,
						pod.PodRef.Name,
						tickTime,
						container.CPU.UsageCoreNanoSeconds,
						1000, // cpu_rate is in millicore
					)

					// Set default zero values for throttled metrics
					periodsKey := getKey(
						"container_cpu_cfs/periods_total",
						namespaceName,
						controllerKind,
						controllerName,
						pod.PodRef.Name,
						container.Name,
					)
					throttleMetrics[periodsKey] = &Metric{
						Name: "container_cpu_cfs/periods_total",
						Type: TypePodContainer,

						NodeName:       node.Name,
						NodeIP:         nodeIP,
						NamespaceName:  namespaceName,
						ControllerName: controllerName,
						ControllerKind: controllerKind,
						ContainerName:  container.Name,
						PodName:        pod.PodRef.Name,
						Timestamp:      tickTime,
						Value:          0,
					}
					throttledSecondsKey := getKey(
						"container_cpu_cfs_throttled/seconds_total",
						namespaceName,
						controllerKind,
						controllerName,
						pod.PodRef.Name,
						container.Name,
					)
					throttleMetrics[throttledSecondsKey] = &Metric{
						Name: "container_cpu_cfs_throttled/seconds_total",
						Type: TypePodContainer,

						NodeName:       node.Name,
						NodeIP:         nodeIP,
						NamespaceName:  namespaceName,
						ControllerName: controllerName,
						ControllerKind: controllerKind,
						ContainerName:  container.Name,
						PodName:        pod.PodRef.Name,
						Timestamp:      tickTime,
						Value:          0,
					}
					throttledPeriodsKey := getKey(
						"container_cpu_cfs_throttled/periods_total",
						namespaceName,
						controllerKind,
						controllerName,
						pod.PodRef.Name,
						container.Name,
					)
					throttleMetrics[throttledPeriodsKey] = &Metric{
						Name: "container_cpu_cfs_throttled/periods_total",
						Type: TypePodContainer,

						NodeName:       node.Name,
						NodeIP:         nodeIP,
						NamespaceName:  namespaceName,
						ControllerName: controllerName,
						ControllerKind: controllerKind,
						ContainerName:  container.Name,
						PodName:        pod.PodRef.Name,
						Timestamp:      tickTime,
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
						logger.Warnw("unable to get cAdvisor",
							"error", err,
							"node", node.Name,
						)
						cadvisorResponse = []byte{}
						return nil
					}
					return errors.Wrapf(
						err,
						"unable to get cadvisor from node %q",
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
				return errors.Wrap(err,
					"unable to read cadvisor response",
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
					if ok {
						controllerName, controllerKind, err := entitiesProvider.FindController(namespaceName, podName)
						if err != nil {
							logger.Errorw(
								"unable to find controller for pod",
								"error", err,
							)
						}
						key := getKey(
							metric.Name,
							namespaceName,
							controllerKind,
							controllerName,
							podName,
							containerName,
						)
						if storedMetric, ok := throttleMetrics[key]; ok {
							storedMetric.Value = int64(value)
						} else {
							logger.Warnw(
								"found a container in cAdvisor response that don't exist at summary response",
								"namespace", namespaceName,
								"pod_name", podName,
								"container_name", containerName,
							)
						}
					}
				}
			}

			for _, metric := range throttleMetrics {
				addMetric(metric)

				rateMetric := *metric
				rateMetric.Name += "_rate"
				var multiplier int64 = 1e9

				// TODO: cleanup when values are sent as floats
				// covert seconds to milliseconds
				if strings.Contains(rateMetric.Name, "seconds") {
					rateMetric.Value *= 1000
					multiplier = 1e6
				}

				// Container metrics use controller name & kind as entity name & kind
				addMetricRate(
					rateMetric.ControllerKind,
					rateMetric.ControllerName,
					multiplier,
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
				logger.Errorw(
					"error while scraping nodes metrics",
					"error", err,
				)
			}
		}
	}

	result = make([]*Metric, 0)

	for _, metrics := range metrics {

		/*
			context = context.
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
		logger.Infof(
			"collected %d measurements with timestamp %s",
			len(metrics),
			metrics[0].Timestamp,
		)
	} else {
		logger.Infof(
			"collected %d measurements",
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
		return nil, errors.New("No previous value")
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
			return errors.Wrapf(err, "max retries exceeded")
		}

		// NOTE max multiplier = 10
		// 300ms -> 600ms -> [...] -> 3000ms -> 300ms
		timeout := kubelet.timeouts.backoff.sleep * time.Duration((try-1)%10+1)

		logger.Warnw("unhandled error occurred",
			"error", err,
			"retryAfter", timeout,
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
			"cpu-%d--memory-%.d",
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
