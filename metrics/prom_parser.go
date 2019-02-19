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

// parseResponse consumes an http.Response and pushes it to the MetricFamily
// channel. It returns when all MetricFamilies are parsed and put on the
// channel.
func parseResponse(
	fetchOnly []string, resp *http.Response, ch chan<- *dto.MetricFamily,
) error {
	mediatype, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err == nil && mediatype == "application/vnd.google.protobuf" &&
		params["encoding"] == "delimited" &&
		params["proto"] == "io.prometheus.client.MetricFamily" {
		defer close(ch)
		for {
			mf := &dto.MetricFamily{}
			if _, err = pbutil.ReadDelimited(resp.Body, mf); err != nil {
				if err == io.EOF {
					break
				}
				return fmt.Errorf("reading metric family protocol buffer failed: %v", err)
			}

			if isAllowed(fetchOnly, mf) {
				ch <- mf
			}

		}
	} else {
		if err := parseReader(fetchOnly, resp.Body, ch); err != nil {
			defer close(ch)
			return err
		}
	}
	return nil
}

// parseReader consumes an io.Reader and pushes it to the MetricFamily
// channel. It returns when all MetricFamilies are parsed and put on the
// channel.
func parseReader(
	fetchOnly []string, in io.Reader, ch chan<- *dto.MetricFamily,
) error {
	defer close(ch)
	// We could do further content-type checks here, but the
	// fallback for now will anyway be the text format
	// version 0.0.4, so just go for it and see if it works.
	var parser expfmt.TextParser
	metricFamilies, err := parser.TextToMetricFamilies(in)
	if err != nil {
		return fmt.Errorf("reading text format failed: %v", err)
	}
	for _, mf := range metricFamilies {
		if isAllowed(fetchOnly, mf) {
			ch <- mf
		}
	}
	return nil
}

// FetchMetricFamilies retrieves metrics from the provided URL, decodes them
// into MetricFamily proto messages, and sends them to the provided channel. It
// returns after all MetricFamilies have been sent.
func FetchMetricFamilies(
	fetchOnly []string, resp *http.Response, ch chan<- *dto.MetricFamily,
) error {
	return parseResponse(fetchOnly, resp, ch)
}

func isAllowed(allowedMetrics []string, mf *dto.MetricFamily) bool {
	if mf == nil {
		return false
	}
	name := ""
	if mf.Name != nil {
		name = *mf.Name
	}

	for _, metric := range allowedMetrics {
		if metric == name {
			return true
		}
	}
	return false
}
