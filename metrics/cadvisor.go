package metrics

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/reconquest/karma-go"
)

// TagsValue a struct to hod tags and values
type TagsValue struct {
	Tags  map[string]string
	Value float64
}

// CadvisorMetrics a struct to hold cadvisor metrics
type CadvisorMetrics map[string][]TagsValue

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

// DecodeCAdvisor decode cadvisor metrics
func DecodeCAdvisor(r io.Reader) (CadvisorMetrics, error) {
	scanner := bufio.NewScanner(r)
	scanner.Split(scanTokens)
	comment := false
	counter := 0
	var (
		metric string
		tags   map[string]string
		value  float64
		ret    = make(CadvisorMetrics)
	)
	for scanner.Scan() {
		scanned := scanner.Text()
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

	if err := scanner.Err(); err != nil {
		return nil, karma.Format(err, "scanner returned an error")
	}
	return ret, nil
}

// ContainerValue a struct to hold a value for a specific container in a pod
type ContainerValue struct {
	podUID        string
	containerName string
	value         float64
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
