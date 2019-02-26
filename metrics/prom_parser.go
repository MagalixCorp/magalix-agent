// Copyright 2018 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// adapted from https://github.com/prometheus/prom2json

package metrics

import (
	"fmt"
	"github.com/matttproud/golang_protobuf_extensions/pbutil"
	"github.com/prometheus/common/expfmt"
	"io"
	"mime"
	"net/http"

	dto "github.com/prometheus/client_model/go"
)

func getValue(m *dto.Metric) float64 {
	if m.Gauge != nil {
		return m.GetGauge().GetValue()
	}
	if m.Counter != nil {
		return m.GetCounter().GetValue()
	}
	if m.Untyped != nil {
		return m.GetUntyped().GetValue()
	}
	return 0.
}

func makeLabels(m *dto.Metric) map[string]string {
	result := map[string]string{}
	for _, lp := range m.Label {
		result[lp.GetName()] = lp.GetValue()
	}
	return result
}

func makeQuantiles(m *dto.Metric) map[string]string {
	result := map[string]string{}
	for _, q := range m.GetSummary().Quantile {
		result[fmt.Sprint(q.GetQuantile())] = fmt.Sprint(q.GetValue())
	}
	return result
}

func makeBuckets(m *dto.Metric) map[string]string {
	result := map[string]string{}
	for _, b := range m.GetHistogram().Bucket {
		result[fmt.Sprint(b.GetUpperBound())] = fmt.Sprint(b.GetCumulativeCount())
	}
	return result
}

// parseReader consumes an io.Reader and pushes it to the MetricFamily
// channel iff it is in allowedMetrics. It returns when all MetricFamilies
// are parsed and put on the channel.
func parseReader(
	allowedMetrics map[string]struct{}, in io.Reader, ch chan<- *dto.MetricFamily,
) error {
	// We could do further content-type checks here, but the
	// fallback for now will anyway be the text format
	// version 0.0.4, so just go for it and see if it works.
	var parser expfmt.TextParser
	metricFamilies, err := parser.TextToMetricFamilies(in)
	if err != nil {
		return fmt.Errorf("reading text format failed: %v", err)
	}
	for _, mf := range metricFamilies {
		if isAllowed(allowedMetrics, mf) {
			ch <- mf
		}
	}
	return nil
}

func isAllowed(allowedMetrics map[string]struct{}, mf *dto.MetricFamily) bool {
	if mf == nil {
		return false
	}
	name := ""
	if mf.Name != nil {
		name = *mf.Name
	}

	_, found := allowedMetrics[name]

	return found
}

// ParseResponse consumes an http.Response and pushes it to the MetricFamily
// channel iff it is in allowedMetrics. It returns when all MetricFamilies
// are parsed and put on the channel.
func ParseResponse(
	allowedMetrics map[string]struct{}, resp *http.Response, ch chan<- *dto.MetricFamily,
) error {
	mediatype, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err == nil && mediatype == "application/vnd.google.protobuf" &&
		params["encoding"] == "delimited" &&
		params["proto"] == "io.prometheus.client.MetricFamily" {
		for {
			mf := &dto.MetricFamily{}
			if _, err = pbutil.ReadDelimited(resp.Body, mf); err != nil {
				if err == io.EOF {
					break
				}
				return fmt.Errorf("reading metric family protocol buffer failed: %v", err)
			}

			if isAllowed(allowedMetrics, mf) {
				ch <- mf
			}

		}
	} else {
		if err := parseReader(allowedMetrics, resp.Body, ch); err != nil {
			return err
		}
	}
	return nil
}
