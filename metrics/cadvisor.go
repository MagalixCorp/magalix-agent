package metrics

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixCorp/magalix-agent/scanner"
	"github.com/MagalixCorp/magalix-agent/utils"
	"github.com/MagalixTechnologies/alltogether-go"
	"github.com/MagalixTechnologies/log-go"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
)

// TagsValue a struct to hod tags and values
type TagsValue struct {
	Tags  map[string]string
	Value float64
}

// CAdvisorMetrics a struct to hold cadvisor metrics
type CAdvisorMetrics map[string][]TagsValue

type CAdvisor struct {
	*log.Logger

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

func (cAdvisor *CAdvisor) GetRawMetrics() (RawMetrics, error) {
	mutex := sync.Mutex{}
	rawMetrics := RawMetrics{}

	getNodeMetrics := func(node kuber.Node) error {
		cAdvisor.Infof(
			nil,
			"{cAdvisor} requesting metrics from node %s",
			node.Name,
		)

		return cAdvisor.withBackoff(func() error {
			cAdvisorResponse, err := http.Get(
				"http://" + cAdvisor.getNodeKubeletAddress(node) + "/metrics/cAdvisorMetrics",
			)
			now := time.Now()

			if cAdvisorResponse != nil && cAdvisorResponse.Body != nil {
				defer func() {
					if err := cAdvisorResponse.Body.Close(); err != nil {
						cAdvisor.Errorf(
							err,
							"unable to close cAdvisorResponse.Body",
						)
					}
				}()
			}

			if err != nil {
				return karma.Format(
					err,
					"{cAdvisor} unable to get cAdvisor from node %q",
					node.Name,
				)
			}

			cAdvisorMetrics, err := decodeCAdvisorResponse(cAdvisorResponse.Body)

			if err != nil {
				return karma.Format(
					err,
					"{cAdvisor} unable to decode cAdvisor response from node %q",
					node.Name,
				)
			}

			for metric, values := range cAdvisorMetrics {
				for _, val := range values {

					var containerName, podName, namespace string
					var applicationID, serviceID, containerID uuid.UUID

					containerName, _ = val.Tags["container_name"]
					podName, _ = val.Tags["pod_name"]
					namespace, _ = val.Tags["namespace"]

					// NOTE: we still add metrics with no bounded entities
					if containerName != "" {
						var container *scanner.Container
						applicationID, serviceID, container, _ = cAdvisor.scanner.FindContainer(namespace, podName,
							containerName)
						containerID = container.ID
					} else if podName != "" {
						applicationID, serviceID, _ = cAdvisor.scanner.FindService(namespace, podName)
					}

					var applicationTag, serviceTag, containerTag *uuid.UUID
					if !applicationID.IsNil() {
						applicationTag = &applicationID
					}
					if !serviceID.IsNil() {
						serviceTag = &serviceID
					}
					if !containerID.IsNil() {
						containerTag = &containerID
					}

					mutex.Lock()
					rawMetrics = append(rawMetrics, &RawMetric{
						Metric: metric,

						Node: node.ID,

						Application: applicationTag,
						Service:     serviceTag,
						Container:   containerTag,

						Tags:  val.Tags,
						Value: val.Value,

						Timestamp: now,
					})
					mutex.Unlock()
				}
			}

			return nil

		})
	}

	nodes := cAdvisor.scanner.GetNodes()
	pr, err := alltogether.NewConcurrentProcessor(nodes, getNodeMetrics)
	if err != nil {
		panic(err)
	}

	errs := pr.Do()
	if !errs.AllNil() {
		// Note: if one node fails we fail safe to allow other node metrics to flow.
		// Note: In cases where pods are replicated across nodes,
		// Note: it means that the metrics are misleading. However, It is the
		// Note: rule of consumers to validate the correctness of the metrics
		// Note: and drop bad points

		for _, err := range errs {
			if err != nil {
				cAdvisor.Errorf(
					err,
					"{cAdvisor} error while scraping nodes metrics",
				)
			}
		}
	}

	return rawMetrics, nil
}
