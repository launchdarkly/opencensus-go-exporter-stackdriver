// Copyright 2017, OpenCensus Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package stackdriver

import (
	"context"
	"fmt"
	"testing"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"github.com/launchdarkly/opencensus-go-exporter-stackdriver/monitoredresource"

	"github.com/golang/protobuf/ptypes/timestamp"
	"github.com/google/go-cmp/cmp"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"google.golang.org/api/option"
	"google.golang.org/genproto/googleapis/api/distribution"
	metricpb "google.golang.org/genproto/googleapis/api/metric"
	monitoredrespb "google.golang.org/genproto/googleapis/api/monitoredres"
	monitoringpb "google.golang.org/genproto/googleapis/monitoring/v3" //nolint: staticcheck
	"google.golang.org/grpc"
	"google.golang.org/protobuf/testing/protocmp"
)

var authOptions = []option.ClientOption{option.WithGRPCConn(&grpc.ClientConn{})}

var testOptions = Options{ProjectID: "opencensus-test", MonitoringClientOptions: authOptions}

func TestRejectBlankProjectID(t *testing.T) {
	ids := []string{"", "     ", " "}
	for _, projectID := range ids {
		opts := Options{ProjectID: projectID, MonitoringClientOptions: authOptions}
		exp, err := newStatsExporter(opts)
		if err == nil || exp != nil {
			t.Errorf("%q ProjectID must be rejected: NewExporter() = %v err = %q", projectID, exp, err)
		}
	}
}

func TestExporter_makeReq(t *testing.T) {
	m := stats.Float64("test-measure", "measure desc", "unit")

	key, err := tag.NewKey("test_key")
	if err != nil {
		t.Fatal(err)
	}

	v := &view.View{
		Name:        "example.com/views/testview",
		Description: "desc",
		TagKeys:     []tag.Key{key},
		Measure:     m,
		Aggregation: view.Count(),
	}

	lastValueView := &view.View{
		Name:        "lasttestview",
		Description: "desc",
		TagKeys:     []tag.Key{key},
		Measure:     m,
		Aggregation: view.LastValue(),
	}

	distView := &view.View{
		Name:        "distview",
		Description: "desc",
		Measure:     m,
		Aggregation: view.Distribution(2, 4, 7),
	}

	start := time.Now()
	end := start.Add(time.Minute)
	count1 := &view.CountData{Value: 10}
	count2 := &view.CountData{Value: 16}
	sum1 := &view.SumData{Value: 5.5}
	sum2 := &view.SumData{Value: -11.1}
	last1 := view.LastValueData{Value: 100}
	last2 := view.LastValueData{Value: 200}
	taskValue := getTaskValue()

	tests := []struct {
		name   string
		projID string
		vd     *view.Data
		want   []*monitoringpb.CreateTimeSeriesRequest //nolint: staticcheck
		opts   Options
	}{
		{
			name:   "count agg + timeline",
			projID: "proj-id",
			vd:     newTestViewData(v, start, end, count1, count2),
			want: []*monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				{
					Name: fmt.Sprintf("projects/%s", "proj-id"),
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/example.com/views/testview",
								Labels: map[string]string{
									"test_key":        "test-value-1",
									opencensusTaskKey: taskValue,
								},
							},
							Resource: &monitoredrespb.MonitoredResource{
								Type: "global",
							},
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{ //nolint: staticcheck
										Int64Value: 10,
									}},
								},
							},
						},
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/example.com/views/testview",
								Labels: map[string]string{
									"test_key":        "test-value-2",
									opencensusTaskKey: taskValue,
								},
							},
							Resource: &monitoredrespb.MonitoredResource{
								Type: "global",
							},
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{ //nolint: staticcheck
										Int64Value: 16,
									}},
								},
							},
						},
					},
				},
			},
		},
		{
			name:   "metric type formatter",
			projID: "proj-id",
			vd:     newTestViewData(v, start, end, sum1, sum2),
			opts: Options{
				GetMetricType: func(v *view.View) string {
					return fmt.Sprintf("external.googleapis.com/%s", v.Name)
				},
			},
			want: []*monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				{
					Name: fmt.Sprintf("projects/%s", "proj-id"),
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &metricpb.Metric{
								Type: "external.googleapis.com/example.com/views/testview",
								Labels: map[string]string{
									"test_key":        "test-value-1",
									opencensusTaskKey: taskValue,
								},
							},
							Resource: &monitoredrespb.MonitoredResource{
								Type: "global",
							},
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{ //nolint: staticcheck
										DoubleValue: 5.5,
									}},
								},
							},
						},
						{
							Metric: &metricpb.Metric{
								Type: "external.googleapis.com/example.com/views/testview",
								Labels: map[string]string{
									"test_key":        "test-value-2",
									opencensusTaskKey: taskValue,
								},
							},
							Resource: &monitoredrespb.MonitoredResource{
								Type: "global",
							},
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{ //nolint: staticcheck
										DoubleValue: -11.1,
									}},
								},
							},
						},
					},
				},
			},
		},
		{
			name:   "sum agg + timeline",
			projID: "proj-id",
			vd:     newTestViewData(v, start, end, sum1, sum2),
			want: []*monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				{
					Name: fmt.Sprintf("projects/%s", "proj-id"),
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/example.com/views/testview",
								Labels: map[string]string{
									"test_key":        "test-value-1",
									opencensusTaskKey: taskValue,
								},
							},
							Resource: &monitoredrespb.MonitoredResource{
								Type: "global",
							},
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{ //nolint: staticcheck
										DoubleValue: 5.5,
									}},
								},
							},
						},
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/example.com/views/testview",
								Labels: map[string]string{
									"test_key":        "test-value-2",
									opencensusTaskKey: taskValue,
								},
							},
							Resource: &monitoredrespb.MonitoredResource{
								Type: "global",
							},
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{ //nolint: staticcheck
										DoubleValue: -11.1,
									}},
								},
							},
						},
					},
				},
			},
		},
		{
			name:   "last value agg",
			projID: "proj-id",
			vd:     newTestViewData(lastValueView, start, end, &last1, &last2),
			want: []*monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				{
					Name: fmt.Sprintf("projects/%s", "proj-id"),
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/lasttestview",
								Labels: map[string]string{
									"test_key":        "test-value-1",
									opencensusTaskKey: taskValue,
								},
							},
							Resource: &monitoredrespb.MonitoredResource{
								Type: "global",
							},
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{ //nolint: staticcheck
										DoubleValue: 100,
									}},
								},
							},
						},
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/lasttestview",
								Labels: map[string]string{
									"test_key":        "test-value-2",
									opencensusTaskKey: taskValue,
								},
							},
							Resource: &monitoredrespb.MonitoredResource{
								Type: "global",
							},
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{ //nolint: staticcheck
										DoubleValue: 200,
									}},
								},
							},
						},
					},
				},
			},
		},
		{
			name:   "dist agg + time window - without zero bucket",
			projID: "proj-id",
			vd:     newTestDistViewData(distView, start, end),
			want: []*monitoringpb.CreateTimeSeriesRequest{{ //nolint: staticcheck
				Name: fmt.Sprintf("projects/%s", "proj-id"),
				TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
					{
						Metric: &metricpb.Metric{
							Type: "custom.googleapis.com/opencensus/distview",
							Labels: map[string]string{
								opencensusTaskKey: taskValue,
							},
						},
						Resource: &monitoredrespb.MonitoredResource{
							Type: "global",
						},
						Points: []*monitoringpb.Point{ //nolint: staticcheck
							{
								Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
									StartTime: &timestamp.Timestamp{
										Seconds: start.Unix(),
										Nanos:   int32(start.Nanosecond()),
									},
									EndTime: &timestamp.Timestamp{
										Seconds: end.Unix(),
										Nanos:   int32(end.Nanosecond()),
									},
								},
								Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DistributionValue{ //nolint: staticcheck
									DistributionValue: &distribution.Distribution{
										Count:                 5,
										Mean:                  3.0,
										SumOfSquaredDeviation: 1.5,
										BucketOptions: &distribution.Distribution_BucketOptions{
											Options: &distribution.Distribution_BucketOptions_ExplicitBuckets{
												ExplicitBuckets: &distribution.Distribution_BucketOptions_Explicit{
													Bounds: []float64{0.0, 2.0, 4.0, 7.0}}}},
										BucketCounts: []int64{0, 2, 2, 1}},
								}},
							},
						},
					},
				},
			}},
		},
		{
			name:   "dist agg + time window + zero bucket",
			projID: "proj-id",
			vd:     newTestDistViewData(distView, start, end),
			want: []*monitoringpb.CreateTimeSeriesRequest{{ //nolint: staticcheck
				Name: fmt.Sprintf("projects/%s", "proj-id"),
				TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
					{
						Metric: &metricpb.Metric{
							Type: "custom.googleapis.com/opencensus/distview",
							Labels: map[string]string{
								opencensusTaskKey: taskValue,
							},
						},
						Resource: &monitoredrespb.MonitoredResource{
							Type: "global",
						},
						Points: []*monitoringpb.Point{ //nolint: staticcheck
							{
								Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
									StartTime: &timestamp.Timestamp{
										Seconds: start.Unix(),
										Nanos:   int32(start.Nanosecond()),
									},
									EndTime: &timestamp.Timestamp{
										Seconds: end.Unix(),
										Nanos:   int32(end.Nanosecond()),
									},
								},
								Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DistributionValue{ //nolint: staticcheck
									DistributionValue: &distribution.Distribution{
										Count:                 5,
										Mean:                  3.0,
										SumOfSquaredDeviation: 1.5,
										BucketOptions: &distribution.Distribution_BucketOptions{
											Options: &distribution.Distribution_BucketOptions_ExplicitBuckets{
												ExplicitBuckets: &distribution.Distribution_BucketOptions_Explicit{
													Bounds: []float64{0.0, 2.0, 4.0, 7.0}}}},
										BucketCounts: []int64{0, 2, 2, 1}},
								}},
							},
						},
					},
				},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.opts
			opts.ProjectID = tt.projID
			opts.MonitoringClientOptions = authOptions
			e, err := newStatsExporter(opts)
			if err != nil {
				t.Fatal(err)
			}
			resps := e.makeReq([]*view.Data{tt.vd}, maxTimeSeriesPerUpload)
			if got, want := len(resps), len(tt.want); got != want {
				t.Fatalf("%v: Exporter.makeReq() returned %d responses; want %d", tt.name, got, want)
			}
			if len(tt.want) == 0 {
				return
			}
			if diff := cmp.Diff(resps, tt.want, protocmp.Transform()); diff != "" {
				t.Errorf("Values differ -got +want: %s", diff)
			}
		})
	}
}

func TestTimeIntervalStaggering(t *testing.T) {
	now := time.Now()

	interval := toValidTimeIntervalpb(now, now)

	if err := interval.StartTime.CheckValid(); err != nil {
		t.Fatalf("unable to convert start time from PB: %v", err)
	}
	start := interval.StartTime.AsTime()

	if err := interval.EndTime.CheckValid(); err != nil {
		t.Fatalf("unable to convert end time to PB: %v", err)
	}
	end := interval.EndTime.AsTime()

	if end.Before(start.Add(time.Millisecond)) {
		t.Fatalf("expected end=%v to be at least %v after start=%v, but it wasn't", end, time.Millisecond, start)
	}
}

func TestExporter_makeReq_batching(t *testing.T) {
	m := stats.Float64("test-measure/makeReq_batching", "measure desc", "unit")

	key, err := tag.NewKey("test_key")
	if err != nil {
		t.Fatal(err)
	}

	v := &view.View{
		Name:        "view",
		Description: "desc",
		TagKeys:     []tag.Key{key},
		Measure:     m,
		Aggregation: view.Count(),
	}

	tests := []struct {
		name      string
		iter      int
		limit     int
		wantReqs  int
		wantTotal int
	}{
		{
			name:      "4 vds; 3 limit",
			iter:      2,
			limit:     3,
			wantReqs:  3,
			wantTotal: 4,
		},
		{
			name:      "4 vds; 4 limit",
			iter:      2,
			limit:     4,
			wantReqs:  2,
			wantTotal: 4,
		},
		{
			name:      "4 vds; 5 limit",
			iter:      2,
			limit:     5,
			wantReqs:  2,
			wantTotal: 4,
		},
	}

	count1 := &view.CountData{Value: 10}
	count2 := &view.CountData{Value: 16}

	for _, tt := range tests {
		var vds []*view.Data
		for i := 0; i < tt.iter; i++ {
			vds = append(vds, newTestViewData(v, time.Now(), time.Now(), count1, count2))
		}

		e, err := newStatsExporter(testOptions)
		if err != nil {
			t.Fatal(err)
		}
		resps := e.makeReq(vds, tt.limit)
		if len(resps) != tt.wantReqs {
			t.Errorf("%v:\ngot %d:: %v;\n\nwant %d requests\n\n", tt.name, len(resps), resps, tt.wantReqs)
		}

		var total int
		for _, resp := range resps {
			total += len(resp.TimeSeries)
		}
		if got, want := total, tt.wantTotal; got != want {
			t.Errorf("%v: len(resps[...].TimeSeries) = %d; want %d", tt.name, got, want)
		}
	}
}

func TestExporter_createMetricDescriptorFromView(t *testing.T) {
	oldCreateMetricDescriptor := createMetricDescriptor

	defer func() {
		createMetricDescriptor = oldCreateMetricDescriptor
	}()

	key, _ := tag.NewKey("test-key-one")
	m := stats.Float64("test-measure/TestExporter_createMetricDescriptorFromView", "measure desc", stats.UnitMilliseconds)

	v := &view.View{
		Name:        "test_view_sum",
		Description: "view_description",
		TagKeys:     []tag.Key{key},
		Measure:     m,
		Aggregation: view.Sum(),
	}

	data := &view.CountData{Value: 0}
	vd := newTestViewData(v, time.Now(), time.Now(), data, data)

	var customLabels Labels
	customLabels.Set("pid", "1234", "Local process identifier")
	customLabels.Set("hostname", "test.example.com", "Local hostname")
	customLabels.Set("a/b/c/host-name", "test.example.com", "Local hostname")

	tests := []struct {
		name string
		opts Options
	}{
		{
			name: "default",
		},
		{
			name: "no default labels",
			opts: Options{DefaultMonitoringLabels: &Labels{}},
		},
		{
			name: "custom default labels",
			opts: Options{DefaultMonitoringLabels: &customLabels},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.opts
			opts.MonitoringClientOptions = authOptions
			opts.ProjectID = "test_project"
			e, err := newStatsExporter(opts)
			if err != nil {
				t.Fatal(err)
			}

			var createCalls int
			createMetricDescriptor = func(ctx context.Context, c *monitoring.MetricClient, mdr *monitoringpb.CreateMetricDescriptorRequest) (*metricpb.MetricDescriptor, error) { //nolint: staticcheck
				createCalls++
				if got, want := mdr.MetricDescriptor.Name, "projects/test_project/metricDescriptors/custom.googleapis.com/opencensus/test_view_sum"; got != want {
					t.Errorf("MetricDescriptor.Name = %q; want %q", got, want)
				}
				if got, want := mdr.MetricDescriptor.Type, "custom.googleapis.com/opencensus/test_view_sum"; got != want {
					t.Errorf("MetricDescriptor.Type = %q; want %q", got, want)
				}
				if got, want := mdr.MetricDescriptor.ValueType, metricpb.MetricDescriptor_DOUBLE; got != want {
					t.Errorf("MetricDescriptor.ValueType = %q; want %q", got, want)
				}
				if got, want := mdr.MetricDescriptor.MetricKind, metricpb.MetricDescriptor_CUMULATIVE; got != want {
					t.Errorf("MetricDescriptor.MetricKind = %q; want %q", got, want)
				}
				if got, want := mdr.MetricDescriptor.Description, "view_description"; got != want {
					t.Errorf("MetricDescriptor.Description = %q; want %q", got, want)
				}
				if got, want := mdr.MetricDescriptor.DisplayName, "OpenCensus/test_view_sum"; got != want {
					t.Errorf("MetricDescriptor.DisplayName = %q; want %q", got, want)
				}
				if got, want := mdr.MetricDescriptor.Unit, stats.UnitMilliseconds; got != want {
					t.Errorf("MetricDescriptor.Unit = %q; want %q", got, want)
				}
				return &metricpb.MetricDescriptor{
					DisplayName: "OpenCensus/test_view_sum",
					Description: "view_description",
					Unit:        stats.UnitMilliseconds,
					Type:        "custom.googleapis.com/opencensus/test_view_sum",
					MetricKind:  metricpb.MetricDescriptor_CUMULATIVE,
					ValueType:   metricpb.MetricDescriptor_DOUBLE,
					Labels:      newLabelDescriptors(e.defaultLabels, vd.View.TagKeys),
				}, nil
			}

			ctx := context.Background()
			if err := e.createMetricDescriptorFromView(ctx, vd.View); err != nil {
				t.Errorf("Exporter.createMetricDescriptorFromView() error = %v", err)
			}
			if err := e.createMetricDescriptorFromView(ctx, vd.View); err != nil {
				t.Errorf("Exporter.createMetricDescriptorFromView() error = %v", err)
			}
			if count := createCalls; count != 1 {
				t.Errorf("createMetricDescriptor needs to be called for once; called %v times", count)
			}
			if count := len(e.metricDescriptors); count != 1 {
				t.Errorf("len(e.metricDescriptors) = %v; want 1", count)
			}
		})
	}
}

func TestExporter_createMetricDescriptorFromView_CountAggregation(t *testing.T) {
	oldCreateMetricDescriptor := createMetricDescriptor

	defer func() {
		createMetricDescriptor = oldCreateMetricDescriptor
	}()

	key, _ := tag.NewKey("test-key-one")
	m := stats.Float64("test-measure/TestExporter_createMetricDescriptorFromView", "measure desc", stats.UnitMilliseconds)

	v := &view.View{
		Name:        "test_view_count",
		Description: "view_description",
		TagKeys:     []tag.Key{key},
		Measure:     m,
		Aggregation: view.Count(),
	}

	data := &view.CountData{Value: 0}
	vd := newTestViewData(v, time.Now(), time.Now(), data, data)

	e := &statsExporter{
		metricDescriptors: make(map[string]bool),
		o:                 Options{ProjectID: "test_project"},
	}

	createMetricDescriptor = func(ctx context.Context, c *monitoring.MetricClient, mdr *monitoringpb.CreateMetricDescriptorRequest) (*metricpb.MetricDescriptor, error) { //nolint: staticcheck
		if got, want := mdr.MetricDescriptor.Name, "projects/test_project/metricDescriptors/custom.googleapis.com/opencensus/test_view_count"; got != want {
			t.Errorf("MetricDescriptor.Name = %q; want %q", got, want)
		}
		if got, want := mdr.MetricDescriptor.Type, "custom.googleapis.com/opencensus/test_view_count"; got != want {
			t.Errorf("MetricDescriptor.Type = %q; want %q", got, want)
		}
		if got, want := mdr.MetricDescriptor.ValueType, metricpb.MetricDescriptor_INT64; got != want {
			t.Errorf("MetricDescriptor.ValueType = %q; want %q", got, want)
		}
		if got, want := mdr.MetricDescriptor.MetricKind, metricpb.MetricDescriptor_CUMULATIVE; got != want {
			t.Errorf("MetricDescriptor.MetricKind = %q; want %q", got, want)
		}
		if got, want := mdr.MetricDescriptor.Description, "view_description"; got != want {
			t.Errorf("MetricDescriptor.Description = %q; want %q", got, want)
		}
		if got, want := mdr.MetricDescriptor.DisplayName, "OpenCensus/test_view_count"; got != want {
			t.Errorf("MetricDescriptor.DisplayName = %q; want %q", got, want)
		}
		if got, want := mdr.MetricDescriptor.Unit, stats.UnitDimensionless; got != want {
			t.Errorf("MetricDescriptor.Unit = %q; want %q", got, want)
		}
		return &metricpb.MetricDescriptor{
			DisplayName: "OpenCensus/test_view_sum",
			Description: "view_description",
			Unit:        stats.UnitDimensionless,
			Type:        "custom.googleapis.com/opencensus/test_view_count",
			MetricKind:  metricpb.MetricDescriptor_CUMULATIVE,
			ValueType:   metricpb.MetricDescriptor_INT64,
			Labels:      newLabelDescriptors(nil, vd.View.TagKeys),
		}, nil
	}
	ctx := context.Background()
	if err := e.createMetricDescriptorFromView(ctx, vd.View); err != nil {
		t.Errorf("Exporter.createMetricDescriptorFromView() error = %v", err)
	}
}

func TestExporter_makeReq_withCustomMonitoredResource(t *testing.T) {
	m := stats.Float64("test-measure/TestExporter_makeReq_withCustomMonitoredResource", "measure desc", "unit")

	key, err := tag.NewKey("test_key")
	if err != nil {
		t.Fatal(err)
	}

	v := &view.View{
		Name:        "testview",
		Description: "desc",
		TagKeys:     []tag.Key{key},
		Measure:     m,
		Aggregation: view.Count(),
	}
	if err := view.Register(v); err != nil {
		t.Fatal(err)
	}
	defer view.Unregister(v)

	start := time.Now()
	end := start.Add(time.Minute)
	count1 := &view.CountData{Value: 10}
	count2 := &view.CountData{Value: 16}
	taskValue := getTaskValue()

	resource := &monitoredrespb.MonitoredResource{
		Type: "gce_instance",
		Labels: map[string]string{
			"project_id":  "proj-id",
			"instance_id": "instance",
			"zone":        "us-west-1a",
		},
	}

	gceInst := &monitoredresource.GCEInstance{
		ProjectID:  "proj-id",
		InstanceID: "instance",
		Zone:       "us-west-1a",
	}

	tests := []struct {
		name string
		opts Options
		vd   *view.Data
		want []*monitoringpb.CreateTimeSeriesRequest //nolint: staticcheck
	}{
		{
			name: "count agg timeline",
			opts: Options{Resource: resource},
			vd:   newTestViewData(v, start, end, count1, count2),
			want: []*monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				{
					Name: fmt.Sprintf("projects/%s", "proj-id"),
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/testview",
								Labels: map[string]string{
									"test_key":        "test-value-1",
									opencensusTaskKey: taskValue,
								},
							},
							Resource: resource,
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{ //nolint: staticcheck
										Int64Value: 10,
									}},
								},
							},
						},
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/testview",
								Labels: map[string]string{
									"test_key":        "test-value-2",
									opencensusTaskKey: taskValue,
								},
							},
							Resource: resource,
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{ //nolint: staticcheck
										Int64Value: 16,
									}},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "with MonitoredResource and labels",
			opts: func() Options {
				var labels Labels
				labels.Set("pid", "1234", "Process identifier")
				return Options{
					MonitoredResource:       gceInst,
					DefaultMonitoringLabels: &labels,
				}
			}(),
			vd: newTestViewData(v, start, end, count1, count2),
			want: []*monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				{
					Name: fmt.Sprintf("projects/%s", "proj-id"),
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/testview",
								Labels: map[string]string{
									"test_key": "test-value-1",
									"pid":      "1234",
								},
							},
							Resource: resource,
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{ //nolint: staticcheck
										Int64Value: 10,
									}},
								},
							},
						},
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/testview",
								Labels: map[string]string{
									"test_key": "test-value-2",
									"pid":      "1234",
								},
							},
							Resource: resource,
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{ //nolint: staticcheck
										Int64Value: 16,
									}},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "custom default monitoring labels",
			opts: func() Options {
				var labels Labels
				labels.Set("pid", "1234", "Process identifier")
				return Options{
					Resource:                resource,
					DefaultMonitoringLabels: &labels,
				}
			}(),
			vd: newTestViewData(v, start, end, count1, count2),
			want: []*monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				{
					Name: fmt.Sprintf("projects/%s", "proj-id"),
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/testview",
								Labels: map[string]string{
									"test_key": "test-value-1",
									"pid":      "1234",
								},
							},
							Resource: resource,
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{ //nolint: staticcheck
										Int64Value: 10,
									}},
								},
							},
						},
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/testview",
								Labels: map[string]string{
									"test_key": "test-value-2",
									"pid":      "1234",
								},
							},
							Resource: resource,
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{ //nolint: staticcheck
										Int64Value: 16,
									}},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "count agg timeline",
			opts: Options{Resource: resource},
			vd:   newTestViewData(v, start, end, count1, count2),
			want: []*monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				{
					Name: fmt.Sprintf("projects/%s", "proj-id"),
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/testview",
								Labels: map[string]string{
									"test_key":        "test-value-1",
									opencensusTaskKey: taskValue,
								},
							},
							Resource: resource,
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{ //nolint: staticcheck
										Int64Value: 10,
									}},
								},
							},
						},
						{
							Metric: &metricpb.Metric{
								Type: "custom.googleapis.com/opencensus/testview",
								Labels: map[string]string{
									"test_key":        "test-value-2",
									opencensusTaskKey: taskValue,
								},
							},
							Resource: resource,
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: &timestamp.Timestamp{
											Seconds: start.Unix(),
											Nanos:   int32(start.Nanosecond()),
										},
										EndTime: &timestamp.Timestamp{
											Seconds: end.Unix(),
											Nanos:   int32(end.Nanosecond()),
										},
									},
									Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{ //nolint: staticcheck
										Int64Value: 16,
									}},
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.opts
			opts.MonitoringClientOptions = authOptions
			opts.ProjectID = "proj-id"
			e, err := NewExporter(opts)
			if err != nil {
				t.Fatal(err)
			}
			resps := e.statsExporter.makeReq([]*view.Data{tt.vd}, maxTimeSeriesPerUpload)
			if got, want := len(resps), len(tt.want); got != want {
				t.Fatalf("%v: Exporter.makeReq() returned %d responses; want %d", tt.name, got, want)
			}
			if len(tt.want) == 0 {
				return
			}
			if diff := cmp.Diff(resps, tt.want, protocmp.Transform()); diff != "" {
				t.Errorf("Requests differ, -got +want: %s", diff)
			}
		})
	}
}

func TestSplitCreateTimeSeriesRequest(t *testing.T) {
	tests := []struct {
		name              string
		req               *monitoringpb.CreateTimeSeriesRequest //nolint: staticcheck
		wantServiceReq    *monitoringpb.CreateTimeSeriesRequest //nolint: staticcheck
		wantNonServiceReq *monitoringpb.CreateTimeSeriesRequest //nolint: staticcheck
	}{
		{
			name: "no service metrics",
			req: &monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				Name: fmt.Sprintf("projects/%s", "proj-id"),
				TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
					{
						Metric: &metricpb.Metric{
							Type: "custom.googleapis.com/opencensus/example.com/testmetric-1",
						},
					},
					{
						Metric: &metricpb.Metric{
							Type: "custom.googleapis.com/opencensus/example.com/testmetric-2",
						},
					},
				},
			},
			wantNonServiceReq: &monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				Name: fmt.Sprintf("projects/%s", "proj-id"),
				TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
					{
						Metric: &metricpb.Metric{
							Type: "custom.googleapis.com/opencensus/example.com/testmetric-1",
						},
					},
					{
						Metric: &metricpb.Metric{
							Type: "custom.googleapis.com/opencensus/example.com/testmetric-2",
						},
					},
				},
			},
		},
		{
			name: "custom and service metrics",
			req: &monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				Name: fmt.Sprintf("projects/%s", "proj-id"),
				TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
					{
						Metric: &metricpb.Metric{
							Type: "kubernetes.io/opencensus/example.com/testmetric-1",
						},
					},
					{
						Metric: &metricpb.Metric{
							Type: "custom.googleapis.com/opencensus/example.com/testmetric-2",
						},
					},
				},
			},
			wantNonServiceReq: &monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				Name: fmt.Sprintf("projects/%s", "proj-id"),
				TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
					{
						Metric: &metricpb.Metric{
							Type: "custom.googleapis.com/opencensus/example.com/testmetric-2",
						},
					},
				},
			},
			wantServiceReq: &monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				Name: fmt.Sprintf("projects/%s", "proj-id"),
				TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
					{
						Metric: &metricpb.Metric{
							Type: "kubernetes.io/opencensus/example.com/testmetric-1",
						},
					},
				},
			},
		},
		{
			name: "only service metrics",
			req: &monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				Name: fmt.Sprintf("projects/%s", "proj-id"),
				TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
					{
						Metric: &metricpb.Metric{
							Type: "kubernetes.io/opencensus/example.com/testmetric-1",
						},
					},
					{
						Metric: &metricpb.Metric{
							Type: "kubernetes.io/opencensus/example.com/testmetric-2",
						},
					},
				},
			},
			wantServiceReq: &monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				Name: fmt.Sprintf("projects/%s", "proj-id"),
				TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
					{
						Metric: &metricpb.Metric{
							Type: "kubernetes.io/opencensus/example.com/testmetric-1",
						},
					},
					{
						Metric: &metricpb.Metric{
							Type: "kubernetes.io/opencensus/example.com/testmetric-2",
						},
					},
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotServiceReq, gotNonServiceReq := splitCreateTimeSeriesRequest(tc.req)
			if diff := cmp.Diff(tc.wantServiceReq, gotServiceReq, protocmp.Transform()); diff != "" {
				t.Errorf("splitCreateTimeSeriesRequest(%v) returned diff (-want +got):\n%s", tc.req, diff)
			}
			if diff := cmp.Diff(tc.wantNonServiceReq, gotNonServiceReq, protocmp.Transform()); diff != "" {
				t.Errorf("splitCreateTimeSeriesRequest(%v) returned diff (-want +got):\n%s", tc.req, diff)
			}
		})
	}
}

func TestSplitTimeSeries(t *testing.T) {
	tests := []struct {
		name             string
		timeSeries       []*monitoringpb.TimeSeries //nolint: staticcheck
		wantServiceTs    []*monitoringpb.TimeSeries //nolint: staticcheck
		wantNonServiceTs []*monitoringpb.TimeSeries //nolint: staticcheck
	}{
		{
			name: "no service metrics",
			timeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
				{
					Metric: &metricpb.Metric{
						Type: "custom.googleapis.com/opencensus/example.com/testmetric-1",
					},
				},
				{
					Metric: &metricpb.Metric{
						Type: "custom.googleapis.com/opencensus/example.com/testmetric-2",
					},
				},
			},
			wantNonServiceTs: []*monitoringpb.TimeSeries{ //nolint: staticcheck
				{
					Metric: &metricpb.Metric{
						Type: "custom.googleapis.com/opencensus/example.com/testmetric-1",
					},
				},
				{
					Metric: &metricpb.Metric{
						Type: "custom.googleapis.com/opencensus/example.com/testmetric-2",
					},
				},
			},
		},
		{
			name: "custom and service metrics",
			timeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
				{
					Metric: &metricpb.Metric{
						Type: "kubernetes.io/opencensus/example.com/testmetric-1",
					},
				},
				{
					Metric: &metricpb.Metric{
						Type: "custom.googleapis.com/opencensus/example.com/testmetric-2",
					},
				},
			},
			wantServiceTs: []*monitoringpb.TimeSeries{ //nolint: staticcheck
				{
					Metric: &metricpb.Metric{
						Type: "kubernetes.io/opencensus/example.com/testmetric-1",
					},
				},
			},
			wantNonServiceTs: []*monitoringpb.TimeSeries{ //nolint: staticcheck
				{
					Metric: &metricpb.Metric{
						Type: "custom.googleapis.com/opencensus/example.com/testmetric-2",
					},
				},
			},
		},
		{
			name: "only service metrics",
			timeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
				{
					Metric: &metricpb.Metric{
						Type: "kubernetes.io/opencensus/example.com/testmetric-1",
					},
				},
				{
					Metric: &metricpb.Metric{
						Type: "kubernetes.io/opencensus/example.com/testmetric-2",
					},
				},
			},
			wantServiceTs: []*monitoringpb.TimeSeries{ //nolint: staticcheck
				{
					Metric: &metricpb.Metric{
						Type: "kubernetes.io/opencensus/example.com/testmetric-1",
					},
				},
				{
					Metric: &metricpb.Metric{
						Type: "kubernetes.io/opencensus/example.com/testmetric-2",
					},
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotServiceTs, gotNonServiceTs := splitTimeSeries(tc.timeSeries)
			if diff := cmp.Diff(tc.wantServiceTs, gotServiceTs, protocmp.Transform()); diff != "" {
				t.Errorf("splitTimeSeries(%v) returned diff for service time series (-want +got):\n%s", tc.timeSeries, diff)
			}
			if diff := cmp.Diff(tc.wantNonServiceTs, gotNonServiceTs, protocmp.Transform()); diff != "" {
				t.Errorf("splitTimeSeries(%v) returned diff for non-service time series (-want +got):\n%s", tc.timeSeries, diff)
			}
		})
	}
}

func TestExporter_customContext(t *testing.T) {
	oldCreateMetricDescriptor := createMetricDescriptor
	oldCreateTimeSeries := createTimeSeries

	defer func() {
		createMetricDescriptor = oldCreateMetricDescriptor
		createTimeSeries = oldCreateTimeSeries
	}()

	var timedOut = 0
	createMetricDescriptor = func(ctx context.Context, c *monitoring.MetricClient, mdr *monitoringpb.CreateMetricDescriptorRequest) (*metricpb.MetricDescriptor, error) { //nolint: staticcheck
		select {
		case <-time.After(1 * time.Second):
			fmt.Println("createMetricDescriptor did not time out")
		case <-ctx.Done():
			timedOut++
		}
		return &metricpb.MetricDescriptor{}, nil
	}
	createTimeSeries = func(ctx context.Context, c *monitoring.MetricClient, ts *monitoringpb.CreateTimeSeriesRequest) error { //nolint: staticcheck
		select {
		case <-time.After(1 * time.Second):
			fmt.Println("createTimeSeries did not time out")
		case <-ctx.Done():
			timedOut++
		}
		return nil
	}

	v := &view.View{
		Name:        "test_view_count",
		Description: "view_description",
		Measure:     stats.Float64("test-measure/TestExporter_createMetricDescriptorFromView", "measure desc", stats.UnitMilliseconds),
		Aggregation: view.Count(),
	}

	data := &view.CountData{Value: 0}
	vd := newTestViewData(v, time.Now(), time.Now(), data, data)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	e := &statsExporter{
		metricDescriptors: make(map[string]bool),
		o:                 Options{ProjectID: "test_project", Context: ctx},
	}
	if err := e.uploadStats([]*view.Data{vd}); err != nil {
		t.Errorf("Exporter.uploadStats() error = %v", err)
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Errorf("expected context to time out; got %v", ctx.Err())
	}
	if timedOut != 2 {
		t.Errorf("expected two functions to time out; got %d", timedOut)
	}
}

func newTestViewData(v *view.View, start, end time.Time, data1, data2 view.AggregationData) *view.Data {
	key, _ := tag.NewKey("test-key")
	tag1 := tag.Tag{Key: key, Value: "test-value-1"}
	tag2 := tag.Tag{Key: key, Value: "test-value-2"}
	return &view.Data{
		View: v,
		Rows: []*view.Row{
			{
				Tags: []tag.Tag{tag1},
				Data: data1,
			},
			{
				Tags: []tag.Tag{tag2},
				Data: data2,
			},
		},
		Start: start,
		End:   end,
	}
}

func newTestDistViewData(v *view.View, start, end time.Time) *view.Data {
	return &view.Data{
		View: v,
		Rows: []*view.Row{
			{Data: &view.DistributionData{
				Count:           5,
				Min:             1,
				Max:             7,
				Mean:            3,
				SumOfSquaredDev: 1.5,
				CountPerBucket:  []int64{2, 2, 1},
			}},
		},
		Start: start,
		End:   end,
	}
}
