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
	allowedMetrics map[string]struct{},
	resp *http.Response,
	bind BindFunc,
) (result map[string]*MetricFamily, err error) {
	mfChan := make(chan *io_prometheus_client.MetricFamily, 1024)

	go func() {
		defer close(mfChan)
		err = ParseResponse(allowedMetrics, resp, mfChan)
	}()

	result = map[string]*MetricFamily{}
	for mf := range mfChan {
		family := toMetricFamily(mf, bind)
		result = appendFamily(result, family)
	}

	return
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

	switch dtoMF.GetType() {
	case io_prometheus_client.MetricType_SUMMARY:
		// TODO: implement summary type converter
	case io_prometheus_client.MetricType_HISTOGRAM:
		// TODO: implement histogram type converter
	case
		io_prometheus_client.MetricType_GAUGE,
		io_prometheus_client.MetricType_COUNTER,
		io_prometheus_client.MetricType_UNTYPED:

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
	for _, family := range b {
		metricName := family.Name
		if uniqueMetric, ok := a[metricName]; !ok {
			a[metricName] = family
		} else {
			uniqueMetric.Values = append(uniqueMetric.Values, family.Values...)
		}
	}

	return a
}
