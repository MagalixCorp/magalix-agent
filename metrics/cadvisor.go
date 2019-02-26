package metrics

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixCorp/magalix-agent/scanner"
	"github.com/MagalixCorp/magalix-agent/utils"
	"github.com/MagalixTechnologies/log-go"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
)

// TODO allow all cAdvisor by default.
//  This is postponed because of the unexpected load on Magalix infra
var allowedMetrics = map[string]struct{}{
	"container_cpu_usage_seconds_total":         {},
	"container_cpu_cfs_periods_total":           {},
	"container_cpu_cfs_throttled_periods_total": {},
	"container_cpu_cfs_throttled_seconds_total": {},

	"container_memory_rss": {},

	"container_fs_usage_bytes": {},
	"container_fs_limit_bytes": {},

	"container_network_receive_bytes_total":   {},
	"container_network_receive_errors_total":  {},
	"container_network_transmit_bytes_total":  {},
	"container_network_transmit_errors_total": {},
}

// TagsValue a struct to hod tags and values
type TagsValue struct {
	Tags  map[string]string
	Value float64
}

// CAdvisorMetrics a struct to hold cadvisor metrics
type CAdvisorMetrics map[string][]TagsValue

type CAdvisor struct {
	*log.Logger

	kubeletClient         *KubeletClient
	scanner               *scanner.Scanner
	backoff               utils.Backoff
	getNodeKubeletAddress func(node kuber.Node) string
}

func scanTokens(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	found := false
	ind := len(data) + 1
	for _, c := range []byte("\n{}") {
		if i := bytes.IndexByte(data, c); i >= 0 {
			found = true
			if i < ind {
				ind = i
			}
		}
	}
	if found {
		return ind + 1, data[0 : ind+1], nil
	}
	// If we're at EOF, we have a final, non-terminated line. Return it.
	if atEOF {
		return len(data), data, nil
	}
	// Request more data.
	return 0, nil, nil
}

func getCAdvisorContainerValue(t TagsValue) (podUID string, containerName string, value float64, ok bool) {
	// container name is empty if not existent
	containerName, _ = t.Tags["container_name"]
	id, ok := t.Tags["id"]
	if !ok {
		return
	}
	if !strings.HasPrefix(id, "/kubepods") {
		return
	}
	podregexp := regexp.MustCompile(`pod[0-9a-f\-]+`)
	podUID = podregexp.FindString(id)[3:]
	if len(podUID) <= 0 {
		return
	}
	// dashes removed
	if len(podUID) == 32 {
		// 6b6035fb-e6a9-11e8-a8ed-42010a8e0004
		podUID = fmt.Sprintf(`%s-%s-%s-%s-%s`, podUID[0:8], podUID[8:12], podUID[12:16], podUID[16:20], podUID[20:32])
	}
	value = t.Value
	ok = true
	return
}

// decodeCAdvisorResponse decode cAdvisor response to CAdvisorMetrics
func decodeCAdvisorResponse(r io.Reader) (CAdvisorMetrics, error) {
	bufScanner := bufio.NewScanner(r)
	bufScanner.Split(scanTokens)
	comment := false
	counter := 0
	var (
		metric string
		tags   map[string]string
		value  float64
		ret    = make(CAdvisorMetrics)
	)
	for bufScanner.Scan() {
		scanned := bufScanner.Text()
		token := strings.TrimSpace(scanned[:len(scanned)-1])
		switch counter % 3 {
		case 0:
			if strings.HasPrefix(token, "#") {
				comment = true
			} else {
				metric = token
			}
		case 1:
			if !comment {
				tags = make(map[string]string)
				// TODO: if there is a comma in the value, this will break
				tagList := strings.Split(token, ",")
				for _, tag := range tagList {
					tagSplit := strings.SplitN(tag, "=", 2)
					tmp, err := strconv.Unquote(tagSplit[1])
					if err != nil {
						return nil, karma.Format(err, "unable to unquote: %s", token)
					}
					tags[tagSplit[0]] = tmp
				}
			}
		case 2:
			if !comment {
				tmp, err := strconv.ParseFloat(token, 64)
				if err != nil {
					return nil, karma.Format(err, "unable to parse float %s", token)
				}
				value = tmp
				v, ok := ret[metric]
				if !ok {
					v = make([]TagsValue, 0)
				}
				v = append(v, TagsValue{
					Tags:  tags,
					Value: value,
				})
				ret[metric] = v
			}
		}
		counter++
		if scanned[len(scanned)-1] == '\n' {
			comment = false
			counter = 0
		}
	}

	if err := bufScanner.Err(); err != nil {
		return nil, karma.Format(err, "bufScanner returned an error")
	}
	return ret, nil
}

func (cAdvisor *CAdvisor) withBackoff(fn func() error) error {
	return utils.WithBackoff(fn, cAdvisor.backoff, cAdvisor.Logger)
}

func (cAdvisor *CAdvisor) GetMetrics(tickTime time.Time) (
	chan *MetricsBatch,
	error,
) {
	batchPipe := make(chan *MetricsBatch, 0)

	scrapeNodeMetrics := func(node *kuber.Node) {
		ctx := karma.
			Describe("node", node.Name).
			Describe("tick_time", tickTime.Format(time.RFC3339))

		cAdvisor.Infof(
			ctx,
			"{cAdvisor} requesting metrics from node %s",
			node.Name,
		)

		var nodeMetrics map[string]*MetricFamily
		var metricsTimestamp time.Time

		err := cAdvisor.withBackoff(func() error {
			response, err := cAdvisor.kubeletClient.Get(node, "metrics/cadvisor")
			metricsTimestamp = time.Now().UTC()

			nodeMetrics, err = ReadPrometheusMetrics(
				allowedMetrics,
				response,
				func(labels map[string]string) (
					entities *Entities, tags map[string]string,
				) {
					entities, tags = cAdvisor.bind(labels)
					if entities != nil {
						entities.Node = &node.ID
					}
					return entities, tags
				},
			)

			if err != nil {
				if strings.Contains(err.Error(), "the server could not find the requested resource") {
					cAdvisor.Warningf(ctx.Reason(err),
						"{cAdvisor} unable to get cAdvisor from node %q",
						node.Name,
					)
					return nil
				} else {
					return ctx.Format(
						err,
						"{cAdvisor} unable to read cAdvisor metrics from node %q",
						node.Name,
					)
				}
			}

			return nil

		})

		if err != nil {
			cAdvisor.Errorf(
				ctx.Reason(err),
				"{cAdvisor} error while scraping node %s metric",
				node.Name,
			)
			return
		}

		ctx = ctx.
			Describe("timestamp", metricsTimestamp.Format(time.RFC3339)).
			Describe("metrics_count", len(nodeMetrics))

		cAdvisor.Infof(
			ctx,
			"{cAdvisor} collected %%v metrics from node %s",
			len(nodeMetrics),
			node.Name,
		)

		if len(nodeMetrics) > 0 {
			batchPipe <- &MetricsBatch{
				Timestamp: metricsTimestamp,
				Metrics:   nodeMetrics,
			}
		}

	}

	go func() {
		defer close(batchPipe)

		// don't wait for the tickTime and assume latest nodes definitions are good
		nodes := cAdvisor.scanner.GetNodes()

		ctx := karma.Describe("tick_time", tickTime.Format(time.RFC3339))
		cAdvisor.Infof(
			ctx,
			"{cAdvisor} requesting metrics",
		)

		wg := sync.WaitGroup{}
		wg.Add(len(nodes))

		for _, node := range nodes {
			go func(node kuber.Node) {
				scrapeNodeMetrics(&node)
				wg.Done()
			}(node)
		}

		wg.Wait()

		cAdvisor.Infof(
			ctx,
			"{cAdvisor} collected metrics",
		)
	}()

	return batchPipe, nil
}

func (cAdvisor *CAdvisor) bind(labels map[string]string) (
	entities *Entities, tags map[string]string,
) {
	var ok bool
	var containerName, podName, namespace string
	var applicationID, serviceID, containerID uuid.UUID

	if containerName, ok = labels["container_name"]; ok {
		delete(labels, "container_name")
	}
	if podName, ok = labels["pod_name"]; ok {
		delete(labels, "pod_name")
	}
	if namespace, ok = labels["namespace"]; ok {
		delete(labels, "namespace")
	}

	// reset flag
	ok = false

	var metricType string

	// NOTE: we still add metrics with no bounded entities!
	if namespace != "" {
		if containerName != "" && containerName != "POD" {
			metricType = TypePodContainer

			var container *scanner.Container
			applicationID, serviceID, container, ok = cAdvisor.scanner.FindContainer(
				namespace, podName, containerName,
			)
			if ok {
				containerID = container.ID
			}
		} else if podName != "" && containerName == "POD" {
			metricType = TypePod
			applicationID, serviceID, ok = cAdvisor.scanner.FindService(namespace, podName)
		}
	} else {
		// TODO: Node level and system containers metrics?
		idLabel, ok := labels["id"]
		if ok {
			if idLabel == "/" {
				metricType = TypeNode
			} else if idLabel != "" {
				metricType = TypeSysContainer
				return nil, labels
			} else {
				//	TODO: is this even possible?
				cAdvisor.Warning(karma.Describe("labels", labels), "empty id field?")
				return nil, labels
			}
		}
	}

	labels["type"] = metricType

	entities = &Entities{}
	if !applicationID.IsNil() {
		entities.Application = &applicationID
	}
	if !serviceID.IsNil() {
		entities.Service = &serviceID
	}
	if !containerID.IsNil() {
		entities.Container = &containerID
	}

	return entities, labels
}

func NewCAdvisor(
	kubeletClient *KubeletClient,
	logger *log.Logger,
	scanner *scanner.Scanner,
	backoff utils.Backoff,
) (*CAdvisor, error) {
	cAdvisor := &CAdvisor{
		Logger: logger,

		kubeletClient: kubeletClient,

		scanner: scanner,
		backoff: backoff,
	}

	return cAdvisor, nil
}
