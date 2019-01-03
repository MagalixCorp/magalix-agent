package metrics

import (
	"github.com/MagalixCorp/magalix-agent/client"
	"time"

	"github.com/MagalixCorp/magalix-agent/scanner"
	"github.com/prometheus/client_model/go"
)

type BindFunc func(labels map[string]string) (entities *Entities, tags map[string]string)

type Prometheus struct {
	client  *client.Client
	scanner *scanner.Scanner

	bind BindFunc
}

func ReadPrometheusMetrics(
	url string,
	certificate string, key string, skipServerCertCheck bool,
	bind BindFunc,
) (result *MetricsBatch, err error) {
	mfChan := make(chan *io_prometheus_client.MetricFamily, 1024)
	timeChan := make(chan *time.Time)

	go func() {
		err = FetchMetricFamilies(url, mfChan, certificate, key, skipServerCertCheck, timeChan)
	}()

	timestamp := <-timeChan

	var metrics []*MetricFamily
	for mf := range mfChan {
		metrics = append(metrics, toFamilies(mf, bind)...)
	}

	return &MetricsBatch{
		Timestamp: *timestamp,
		Metrics:   metrics,
	}, err
}

// NewFamily consumes a MetricFamily and transforms it to the local Family type.
func toFamilies(
	dtoMF *io_prometheus_client.MetricFamily,
	bind BindFunc,
) []*MetricFamily {

	var mfs []*MetricFamily

	if dtoMF.GetType() == io_prometheus_client.MetricType_SUMMARY {

	} else if dtoMF.GetType() == io_prometheus_client.MetricType_HISTOGRAM {

	} else {
		mf := &MetricFamily{
			//Time:    time.Now(),
			Name: dtoMF.GetName(),
			Help: dtoMF.GetHelp(),
			Type: dtoMF.GetType().String(),

			Values: make([]*MetricValue, len(dtoMF.Metric)),
		}

		uniqueTags := map[string]bool{}

		for i, m := range dtoMF.Metric {
			labels := makeLabels(m)
			entities, labels := bind(labels)
			for label := range labels {
				uniqueTags[label] = true
			}

			mf.Values[i] = &MetricValue{
				Entities: entities,

				Tags:  labels,
				Value: getValue(m),
			}
		}

		metricTags := make([]string, len(uniqueTags))
		i := 0
		for tag := range uniqueTags {
			metricTags[i] = tag
			i++
		}
		mf.Tags = metricTags

		mfs = append(mfs, mf)
	}

	return mfs
}

func FlattenMetricsBatches(in []*MetricsBatch) (result *MetricsBatch) {
	if len(in) == 0 {
		return
	}

	result = &MetricsBatch{}
	result.Timestamp = in[0].Timestamp

	unique := map[string]*MetricFamily{}
	for _, batch := range in {
		for _, metric := range batch.Metrics {
			if uniqueMetric, ok := unique[metric.Name]; !ok {
				unique[metric.Name] = metric
			} else {
				uniqueMetric.Values = append(uniqueMetric.Values, metric.Values...)
			}
		}
	}

	result.Metrics = make([]*MetricFamily, len(unique))

	i := 0
	for _, item := range unique {
		result.Metrics[i] = item
		i++
	}

	return
}
