package metrics

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// tagsValue a struct to hod tags and values
type tagsValue struct {
	Tags  map[string]string
	Value float64
}

// cAdvisorMetrics a struct to hold cadvisor metrics
type cAdvisorMetrics map[string][]tagsValue

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

func getCAdvisorContainerValue(t tagsValue) (string, string, string, float64, bool) {
	// container name is empty if not existent
	containerName, ok := t.Tags["container_name"]
	if !ok || containerName == "" || containerName == "POD" {
		return "", "", "", 0, false
	}

	namespace, ok := t.Tags["namespace"]
	if !ok || namespace == "" {
		return "", "", "", 0, false
	}

	podName, ok := t.Tags["pod_name"]
	if !ok || podName == "" {
		podName, ok = t.Tags["pod"]
		if !ok || podName == "" {
			return "", "", "", 0, false
		}
	}

	value := t.Value
	return namespace, podName, containerName, value, true
}

// decodeCAdvisorResponse decode cAdvisor response to cAdvisorMetrics
func decodeCAdvisorResponse(r io.Reader) (cAdvisorMetrics, error) {
	bufScanner := bufio.NewScanner(r)
	bufScanner.Split(scanTokens)
	comment := false
	counter := 0
	var (
		metric string
		tags   map[string]string
		value  float64
		ret    = make(cAdvisorMetrics)
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
						return nil, fmt.Errorf("unable to unquote: %s, error: %w", token, err)
					}
					tags[tagSplit[0]] = tmp
				}
			}
		case 2:
			if !comment {
				parts := strings.Split(token, " ")
				token = parts[0]
				tmp, err := strconv.ParseFloat(token, 64)
				if err != nil {
					return nil, fmt.Errorf("unable to parse float %s, error: %w", token, err)
				}
				value = tmp
				v, ok := ret[metric]
				if !ok {
					v = make([]tagsValue, 0)
				}
				v = append(v, tagsValue{
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
		return nil, fmt.Errorf("bufScanner returned an error, error: %w", err)
	}
	return ret, nil
}
