package metrics

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecodeCAdvisor(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    CadvisorMetrics
		wantErr bool
	}{
		{
			name: "test empty input",
			in:   "",
			want: CadvisorMetrics{},
		},
		{
			name: "test regular input",
			in: `
			container_cpu_cfs_throttled_periods_total{container_name="",id="/kubepods/burstable/pod6b6035fb-e6a9-11e8-a8ed-42010a8e0004",image="",name="",namespace="",pod_name=""} 53328
container_cpu_cfs_throttled_periods_total{container_name="",id="/kubepods/burstable/pod7656b510-e6a9-11e8-a8ed-42010a8e0004",image="",name="",namespace="",pod_name=""} 14557
container_cpu_cfs_throttled_periods_total{container_name="",id="/kubepods/burstable/podc3b1b941-e5eb-11e8-a8ed-42010a8e0004",image="",name="",namespace="",pod_name=""} 321216
container_cpu_cfs_throttled_periods_total{container_name="",id="/kubepods/burstable/podfb95fb02-e6aa-11e8-a8ed-42010a8e0004",image="",name="",namespace="",pod_name=""} 4577
container_cpu_cfs_throttled_seconds_total{container_name="",id="/kubepods/burstable/pod6b6035fb-e6a9-11e8-a8ed-42010a8e0004",image="",name="",namespace="",pod_name=""} 3357.740971059
`,
			want: CadvisorMetrics{
				"container_cpu_cfs_throttled_periods_total": []TagsValue{
					{
						Tags: map[string]string{
							"container_name": "",
							"id":             "/kubepods/burstable/pod6b6035fb-e6a9-11e8-a8ed-42010a8e0004",
							"image":          "",
							"name":           "",
							"namespace":      "",
							"pod_name":       "",
						},
						Value: 53328,
					},
					{
						Tags: map[string]string{
							"container_name": "",
							"id":             "/kubepods/burstable/pod7656b510-e6a9-11e8-a8ed-42010a8e0004",
							"image":          "",
							"name":           "",
							"namespace":      "",
							"pod_name":       "",
						},
						Value: 14557,
					},
					{
						Tags: map[string]string{
							"container_name": "",
							"id":             "/kubepods/burstable/podc3b1b941-e5eb-11e8-a8ed-42010a8e0004",
							"image":          "",
							"name":           "",
							"namespace":      "",
							"pod_name":       "",
						},
						Value: 321216,
					},
					{
						Tags: map[string]string{
							"container_name": "",
							"id":             "/kubepods/burstable/podfb95fb02-e6aa-11e8-a8ed-42010a8e0004",
							"image":          "",
							"name":           "",
							"namespace":      "",
							"pod_name":       "",
						},
						Value: 4577,
					},
				},
				"container_cpu_cfs_throttled_seconds_total": []TagsValue{
					{
						Tags: map[string]string{
							"container_name": "",
							"id":             "/kubepods/burstable/pod6b6035fb-e6a9-11e8-a8ed-42010a8e0004",
							"image":          "",
							"name":           "",
							"namespace":      "",
							"pod_name":       "",
						},
						Value: 3357.740971059,
					},
				},
			},
		},
		{
			name: "test comments",
			in: `# asdsad asd asdasd
			# asdsad {asd} asdasd
			container_cpu_cfs_throttled_periods_total{container_name="",id="/kubepods/burstable/pod6b6035fb-e6a9-11e8-a8ed-42010a8e0004",image="",name="",namespace="",pod_name=""} 53328
			#container_cpu_cfs_throttled_periods_total{container_name="",id="/kubepods/burstable/pod6b603544-e6a9-11e8-a8ed-42010a8e0004",image="",name="",namespace="",pod_name=""} 53328
container_cpu_cfs_throttled_periods_total{container_name="",id="/kubepods/burstable/pod7656b510-e6a9-11e8-a8ed-42010a8e0004",image="",name="",namespace="",pod_name=""} 14557
container_cpu_cfs_throttled_periods_total{container_name="",id="/kubepods/burstable/podc3b1b941-e5eb-11e8-a8ed-42010a8e0004",image="",name="",namespace="",pod_name=""} 321216
container_cpu_cfs_throttled_periods_total{container_name="",id="/kubepods/burstable/podfb95fb02-e6aa-11e8-a8ed-42010a8e0004",image="",name="",namespace="",pod_name=""} 4577
container_cpu_cfs_throttled_seconds_total{container_name="",id="/kubepods/burstable/pod6b6035fb-e6a9-11e8-a8ed-42010a8e0004",image="",name="",namespace="",pod_name=""} 3357.740971059
`,
			want: CadvisorMetrics{
				"container_cpu_cfs_throttled_periods_total": []TagsValue{
					{
						Tags: map[string]string{
							"container_name": "",
							"id":             "/kubepods/burstable/pod6b6035fb-e6a9-11e8-a8ed-42010a8e0004",
							"image":          "",
							"name":           "",
							"namespace":      "",
							"pod_name":       "",
						},
						Value: 53328,
					},
					{
						Tags: map[string]string{
							"container_name": "",
							"id":             "/kubepods/burstable/pod7656b510-e6a9-11e8-a8ed-42010a8e0004",
							"image":          "",
							"name":           "",
							"namespace":      "",
							"pod_name":       "",
						},
						Value: 14557,
					},
					{
						Tags: map[string]string{
							"container_name": "",
							"id":             "/kubepods/burstable/podc3b1b941-e5eb-11e8-a8ed-42010a8e0004",
							"image":          "",
							"name":           "",
							"namespace":      "",
							"pod_name":       "",
						},
						Value: 321216,
					},
					{
						Tags: map[string]string{
							"container_name": "",
							"id":             "/kubepods/burstable/podfb95fb02-e6aa-11e8-a8ed-42010a8e0004",
							"image":          "",
							"name":           "",
							"namespace":      "",
							"pod_name":       "",
						},
						Value: 4577,
					},
				},
				"container_cpu_cfs_throttled_seconds_total": []TagsValue{
					{
						Tags: map[string]string{
							"container_name": "",
							"id":             "/kubepods/burstable/pod6b6035fb-e6a9-11e8-a8ed-42010a8e0004",
							"image":          "",
							"name":           "",
							"namespace":      "",
							"pod_name":       "",
						},
						Value: 3357.740971059,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeCAdvisor(strings.NewReader(tt.in))
			if (err != nil) != tt.wantErr {
				t.Errorf("Decode() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Decode() = %v, want %v", got, tt.want)
			}
		})
	}
}
