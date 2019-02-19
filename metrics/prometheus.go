package metrics

import (
	"github.com/MagalixCorp/magalix-agent/client"
	"github.com/MagalixCorp/magalix-agent/scanner"
	"github.com/prometheus/client_model/go"
	"net/http"
)

type BindFunc func(labels map[string]string) (entities *Entities, tags map[string]string)

type Prometheus struct {
	client  *client.Client
	scanner *scanner.Scanner

	bind BindFunc
}

func ReadPrometheusMetrics(
	fetchOnly []string,
	resp *http.Response,
	bind BindFunc,
) (result *MetricsBatch, err error) {
	mfChan := make(chan *io_prometheus_client.MetricFamily, 1024)

	go func() {
		err = FetchMetricFamilies(fetchOnly, resp, mfChan)
	}()

	metrics := map[string]*MetricFamily{}
	for mf := range mfChan {
		family := toMetricFamily(mf, bind)
		metrics = appendFamily(metrics, family)
	}

	return &MetricsBatch{
		Metrics: metrics,
	}, err
}

// toMetricFamily consumes a MetricFamily and transforms it to the local Family type.
func toMetricFamily(
	dtoMF *io_prometheus_client.MetricFamily,
	bind BindFunc,
) *MetricFamily {

	mf := &MetricFamily{
		//Time:    time.Now(),
		Name: dtoMF.GetName(),
		Help: dtoMF.GetHelp(),
		Type: dtoMF.GetType().String(),

		Values: make([]*MetricValue, 0),
	}

	if dtoMF.GetType() == io_prometheus_client.MetricType_SUMMARY {

	} else if dtoMF.GetType() == io_prometheus_client.MetricType_HISTOGRAM {

	} else {

		uniqueTags := map[string]bool{}

		for _, m := range dtoMF.Metric {
			labels := makeLabels(m)
			entities, labels := bind(labels)
			for label := range labels {
				uniqueTags[label] = true
			}

			if entities != nil {
				mf.Values = append(
					mf.Values,
					&MetricValue{
						Entities: entities,

						Tags:  labels,
						Value: getValue(m),
					},
				)
			}
		}

		metricTags := make([]string, len(uniqueTags))
		i := 0
		for tag := range uniqueTags {
			metricTags[i] = tag
			i++
		}
		mf.Tags = metricTags
	}

	return mf
}

func appendFamily(
	metricFamilies map[string]*MetricFamily,
	families ...*MetricFamily,
) map[string]*MetricFamily {
	for _, family := range families {
		metricName := family.Name
		if uniqueMetric, ok := metricFamilies[metricName]; !ok {
			metricFamilies[metricName] = family
		} else {
			uniqueMetric.Values = append(uniqueMetric.Values, family.Values...)
		}
	}

	return metricFamilies
}

func mergeFamilies(
	a map[string]*MetricFamily,
	b map[string]*MetricFamily,
) map[string]*MetricFamily {
	values := make([]*MetricFamily, len(b))
	i := 0
	for _, v := range b {
		values[i] = v
		i++
	}
	return appendFamily(a, values...)
}

func FlattenMetricsBatches(in []*MetricsBatch) (result *MetricsBatch) {
	if len(in) == 0 {
		return
	}

	result = &MetricsBatch{
		Timestamp: in[0].Timestamp,
		Metrics:   map[string]*MetricFamily{},
	}

	for _, batch := range in {
		for _, metric := range batch.Metrics {
			result.Metrics = appendFamily(result.Metrics, metric)
		}
	}

	return
}
