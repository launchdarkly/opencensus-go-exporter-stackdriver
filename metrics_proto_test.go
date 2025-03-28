// Copyright 2018, OpenCensus Authors
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
	"strings"
	"testing"

	resourcepb "github.com/census-instrumentation/opencensus-proto/gen-go/resource/v1"
	"github.com/golang/protobuf/ptypes/timestamp"
	"google.golang.org/api/option"
	distributionpb "google.golang.org/genproto/googleapis/api/distribution"
	labelpb "google.golang.org/genproto/googleapis/api/label"
	googlemetricpb "google.golang.org/genproto/googleapis/api/metric"
	monitoredrespb "google.golang.org/genproto/googleapis/api/monitoredres"
	monitoringpb "google.golang.org/genproto/googleapis/monitoring/v3" //nolint: staticcheck
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/testing/protocmp"

	metricspb "github.com/census-instrumentation/opencensus-proto/gen-go/metrics/v1"
	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/go-cmp/cmp"
	"go.opencensus.io/resource/resourcekeys"
)

func TestExportTimeSeriesWithDifferentLabels(t *testing.T) {
	server, addr, doneFn := createFakeServer(t)
	defer doneFn()

	// Now create a gRPC connection to the agent.
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to make a gRPC connection to the agent: %v", err)
	}
	defer conn.Close()

	// Finally create the OpenCensus stats exporter
	exporterOptions := Options{
		ProjectID:               "equivalence",
		MonitoringClientOptions: []option.ClientOption{option.WithGRPCConn(conn)},

		// Set empty labels to avoid the opencensus-task
		DefaultMonitoringLabels: &Labels{},
		MapResource:             DefaultMapResource,
	}
	se, err := newStatsExporter(exporterOptions)
	if err != nil {
		t.Fatalf("Failed to create the statsExporter: %v", err)
	}

	startTimestamp := &timestamp.Timestamp{
		Seconds: 1543160298,
		Nanos:   100000090,
	}
	endTimestamp := &timestamp.Timestamp{
		Seconds: 1543160298,
		Nanos:   101000090,
	}

	// Generate the proto Metrics.
	var metricPbs []*metricspb.Metric
	metricPbs = append(metricPbs,
		&metricspb.Metric{
			MetricDescriptor: &metricspb.MetricDescriptor{
				Name:        "ocagent.io/calls",
				Description: "The number of the various calls",
				LabelKeys: []*metricspb.LabelKey{
					{
						Key: "empty_key",
					},
					{
						Key: "operation_type",
					},
				},
				Unit: "1",
				Type: metricspb.MetricDescriptor_CUMULATIVE_INT64,
			},
			Timeseries: []*metricspb.TimeSeries{
				{
					StartTimestamp: startTimestamp,
					LabelValues: []*metricspb.LabelValue{
						{
							Value:    "",
							HasValue: true,
						},
						{
							Value:    "test_1",
							HasValue: true,
						},
					},
					Points: []*metricspb.Point{
						{
							Timestamp: endTimestamp,
							Value:     &metricspb.Point_Int64Value{Int64Value: int64(1)},
						},
					},
				},
				{
					StartTimestamp: startTimestamp,
					LabelValues: []*metricspb.LabelValue{
						{
							Value:    "",
							HasValue: true,
						},
						{
							Value:    "test_2",
							HasValue: true,
						},
					},
					Points: []*metricspb.Point{
						{
							Timestamp: endTimestamp,
							Value:     &metricspb.Point_Int64Value{Int64Value: int64(1)},
						},
					},
				},
			},
		})

	var wantTimeSeries []*monitoringpb.CreateTimeSeriesRequest                     //nolint: staticcheck
	wantTimeSeries = append(wantTimeSeries, &monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
		Name: "projects/equivalence",
		TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
			{
				Metric: &googlemetricpb.Metric{
					Type: "custom.googleapis.com/opencensus/ocagent.io/calls",
					Labels: map[string]string{
						"empty_key":      "",
						"operation_type": "test_1",
					},
				},
				Resource: &monitoredrespb.MonitoredResource{
					Type: "global",
				},
				MetricKind: googlemetricpb.MetricDescriptor_CUMULATIVE,
				ValueType:  googlemetricpb.MetricDescriptor_INT64,
				Points: []*monitoringpb.Point{ //nolint: staticcheck
					{
						Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
							StartTime: startTimestamp,
							EndTime:   endTimestamp,
						},
						Value: &monitoringpb.TypedValue{ //nolint: staticcheck
							Value: &monitoringpb.TypedValue_Int64Value{
								Int64Value: 1,
							},
						},
					},
				},
			},
			{
				Metric: &googlemetricpb.Metric{
					Type: "custom.googleapis.com/opencensus/ocagent.io/calls",
					Labels: map[string]string{
						"empty_key":      "",
						"operation_type": "test_2",
					},
				},
				Resource: &monitoredrespb.MonitoredResource{
					Type: "global",
				},
				MetricKind: googlemetricpb.MetricDescriptor_CUMULATIVE,
				ValueType:  googlemetricpb.MetricDescriptor_INT64,
				Points: []*monitoringpb.Point{ //nolint: staticcheck
					{
						Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
							StartTime: startTimestamp,
							EndTime:   endTimestamp,
						},
						Value: &monitoringpb.TypedValue{ //nolint: staticcheck
							Value: &monitoringpb.TypedValue_Int64Value{
								Int64Value: 1,
							},
						},
					},
				},
			},
		},
	})

	// Export the proto Metrics to the Stackdriver backend.
	dropped, err := se.PushMetricsProto(context.Background(), nil, nil, metricPbs)
	if dropped != 0 || err != nil {
		t.Fatalf("Error pushing metrics, dropped:%d err:%v", dropped, err)
	}

	var gotTimeSeries []*monitoringpb.CreateTimeSeriesRequest                             //nolint: staticcheck
	server.forEachStackdriverTimeSeries(func(sdt *monitoringpb.CreateTimeSeriesRequest) { //nolint: staticcheck
		gotTimeSeries = append(gotTimeSeries, sdt)
	})

	requireTimeSeriesRequestEqual(t, gotTimeSeries, wantTimeSeries)
}

func TestProtoMetricToCreateTimeSeriesRequest(t *testing.T) {
	startTimestamp := &timestamp.Timestamp{
		Seconds: 1543160298,
		Nanos:   100000090,
	}
	endTimestamp := &timestamp.Timestamp{
		Seconds: 1543160298,
		Nanos:   101000090,
	}

	tests := []struct {
		name          string
		in            *metricspb.Metric
		want          []*monitoringpb.CreateTimeSeriesRequest //nolint: staticcheck
		wantErr       string
		statsExporter *statsExporter
	}{
		{
			name: "Test converting Distribution",
			in: &metricspb.Metric{
				MetricDescriptor: &metricspb.MetricDescriptor{
					Name:        "with_metric_descriptor",
					Description: "This is a test",
					Unit:        "By",
				},
				Timeseries: []*metricspb.TimeSeries{
					{
						StartTimestamp: startTimestamp,
						Points: []*metricspb.Point{
							{
								Timestamp: endTimestamp,
								Value: &metricspb.Point_DistributionValue{
									DistributionValue: &metricspb.DistributionValue{
										Count:                 1,
										Sum:                   11.9,
										SumOfSquaredDeviation: 0,
										Buckets: []*metricspb.DistributionValue_Bucket{
											{Count: 1}, {}, {}, {},
										},
										BucketOptions: &metricspb.DistributionValue_BucketOptions{
											Type: &metricspb.DistributionValue_BucketOptions_Explicit_{
												Explicit: &metricspb.DistributionValue_BucketOptions_Explicit{
													// Without zero bucket in
													Bounds: []float64{10, 20, 30, 40},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			statsExporter: &statsExporter{
				o: Options{ProjectID: "foo", MapResource: DefaultMapResource},
			},
			want: []*monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				{
					Name: "projects/foo",
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &googlemetricpb.Metric{
								Type:   "custom.googleapis.com/opencensus/with_metric_descriptor",
								Labels: nil,
							},
							Resource: &monitoredrespb.MonitoredResource{
								Type: "global",
							},
							MetricKind: googlemetricpb.MetricDescriptor_CUMULATIVE,
							ValueType:  googlemetricpb.MetricDescriptor_DISTRIBUTION,
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: startTimestamp,
										EndTime:   endTimestamp,
									},
									Value: &monitoringpb.TypedValue{ //nolint: staticcheck
										Value: &monitoringpb.TypedValue_DistributionValue{
											DistributionValue: &distributionpb.Distribution{
												Count:                 1,
												Mean:                  11.9,
												SumOfSquaredDeviation: 0,
												BucketCounts:          []int64{0, 1, 0, 0, 0},
												BucketOptions: &distributionpb.Distribution_BucketOptions{
													Options: &distributionpb.Distribution_BucketOptions_ExplicitBuckets{
														ExplicitBuckets: &distributionpb.Distribution_BucketOptions_Explicit{
															Bounds: []float64{0, 10, 20, 30, 40},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "Test some label keys don't have values",
			in: &metricspb.Metric{
				MetricDescriptor: &metricspb.MetricDescriptor{
					Name:        "with_metric_descriptor_2",
					Description: "This is a test",
					Unit:        "By",
					LabelKeys:   []*metricspb.LabelKey{{Key: "key1"}, {Key: "key2"}, {Key: "key3"}},
				},
				Timeseries: []*metricspb.TimeSeries{
					{
						StartTimestamp: startTimestamp,
						LabelValues:    []*metricspb.LabelValue{{}, {}, {HasValue: true, Value: "val3"}},
						Points: []*metricspb.Point{
							{
								Timestamp: endTimestamp,
								Value: &metricspb.Point_DoubleValue{
									DoubleValue: 25.0,
								},
							},
						},
					},
				},
			},
			statsExporter: &statsExporter{
				o: Options{ProjectID: "foo", MapResource: DefaultMapResource},
			},
			want: []*monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				{
					Name: "projects/foo",
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &googlemetricpb.Metric{
								Type:   "custom.googleapis.com/opencensus/with_metric_descriptor_2",
								Labels: map[string]string{"key3": "val3"},
							},
							Resource: &monitoredrespb.MonitoredResource{
								Type: "global",
							},
							MetricKind: googlemetricpb.MetricDescriptor_CUMULATIVE,
							ValueType:  googlemetricpb.MetricDescriptor_DISTRIBUTION,
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: startTimestamp,
										EndTime:   endTimestamp,
									},
									Value: &monitoringpb.TypedValue{ //nolint: staticcheck
										Value: &monitoringpb.TypedValue_DoubleValue{
											DoubleValue: 25.0,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	seenResources := make(map[*resourcepb.Resource]*monitoredrespb.MonitoredResource)

	for i, tt := range tests {
		se := tt.statsExporter
		if se == nil {
			se = new(statsExporter)
		}
		allTss, err := protoMetricToTimeSeries(context.Background(), se, se.getResource(nil, tt.in, seenResources), tt.in)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("#%v: unmatched error. Got\n\t%v\nWant\n\t%v", tt.name, err, tt.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("#%v: unexpected error: %v", tt.name, err)
			continue
		}

		got := se.combineTimeSeriesToCreateTimeSeriesRequest(allTss)
		// Our saving grace is serialization equality since some
		// unexported fields could be present in the various values.
		if diff := cmpTSReqs(got, tt.want); diff != "" {
			t.Fatalf("Test %d failed. Unexpected CreateTimeSeriesRequests -got +want: %s", i, diff)
		}
	}
}

func TestProtoMetricWithDifferentResource(t *testing.T) {
	startTimestamp := &timestamp.Timestamp{
		Seconds: 1543160298,
		Nanos:   100000090,
	}
	endTimestamp := &timestamp.Timestamp{
		Seconds: 1543160298,
		Nanos:   101000090,
	}

	seenResources := make(map[*resourcepb.Resource]*monitoredrespb.MonitoredResource)

	tests := []struct {
		in            *metricspb.Metric
		want          []*monitoringpb.CreateTimeSeriesRequest //nolint: staticcheck
		wantErr       string
		statsExporter *statsExporter
	}{
		{
			in: &metricspb.Metric{
				MetricDescriptor: &metricspb.MetricDescriptor{
					Name:        "with_container_resource",
					Description: "This is a test",
					Unit:        "By",
					Type:        metricspb.MetricDescriptor_CUMULATIVE_INT64,
				},
				Resource: &resourcepb.Resource{
					Type: resourcekeys.ContainerType,
					Labels: map[string]string{
						resourcekeys.K8SKeyClusterName:   "cluster1",
						resourcekeys.K8SKeyPodName:       "pod1",
						resourcekeys.K8SKeyNamespaceName: "namespace1",
						resourcekeys.ContainerKeyName:    "container-name1",
						resourcekeys.CloudKeyZone:        "zone1",
					},
				},
				Timeseries: []*metricspb.TimeSeries{
					{
						StartTimestamp: startTimestamp,
						Points: []*metricspb.Point{
							{
								Timestamp: endTimestamp,
								Value: &metricspb.Point_Int64Value{
									Int64Value: 1,
								},
							},
						},
					},
				},
			},
			statsExporter: &statsExporter{
				o: Options{ProjectID: "foo", MapResource: DefaultMapResource},
			},
			want: []*monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				{
					Name: "projects/foo",
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &googlemetricpb.Metric{
								Type:   "custom.googleapis.com/opencensus/with_container_resource",
								Labels: nil,
							},
							Resource: &monitoredrespb.MonitoredResource{
								Type: "k8s_container",
								Labels: map[string]string{
									"location":       "zone1",
									"cluster_name":   "cluster1",
									"namespace_name": "namespace1",
									"pod_name":       "pod1",
									"container_name": "container-name1",
								},
							},
							MetricKind: googlemetricpb.MetricDescriptor_CUMULATIVE,
							ValueType:  googlemetricpb.MetricDescriptor_INT64,
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: startTimestamp,
										EndTime:   endTimestamp,
									},
									Value: &monitoringpb.TypedValue{ //nolint: staticcheck
										Value: &monitoringpb.TypedValue_Int64Value{
											Int64Value: 1,
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			in: &metricspb.Metric{
				MetricDescriptor: &metricspb.MetricDescriptor{
					Name:        "with_gce_resource",
					Description: "This is a test",
					Unit:        "By",
					Type:        metricspb.MetricDescriptor_CUMULATIVE_INT64,
				},
				Resource: &resourcepb.Resource{
					Type: resourcekeys.CloudType,
					Labels: map[string]string{
						resourcekeys.CloudKeyProvider: resourcekeys.CloudProviderGCP,
						resourcekeys.HostKeyID:        "inst1",
						resourcekeys.CloudKeyZone:     "zone1",
					},
				},
				Timeseries: []*metricspb.TimeSeries{
					{
						StartTimestamp: startTimestamp,
						Points: []*metricspb.Point{
							{
								Timestamp: endTimestamp,
								Value: &metricspb.Point_Int64Value{
									Int64Value: 1,
								},
							},
						},
					},
				},
			},
			statsExporter: &statsExporter{
				o: Options{ProjectID: "foo", MapResource: DefaultMapResource},
			},
			want: []*monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				{
					Name: "projects/foo",
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &googlemetricpb.Metric{
								Type:   "custom.googleapis.com/opencensus/with_gce_resource",
								Labels: nil,
							},
							Resource: &monitoredrespb.MonitoredResource{
								Type: "gce_instance",
								Labels: map[string]string{
									"instance_id": "inst1",
									"zone":        "zone1",
								},
							},
							MetricKind: googlemetricpb.MetricDescriptor_CUMULATIVE,
							ValueType:  googlemetricpb.MetricDescriptor_INT64,
							Points: []*monitoringpb.Point{ //nolint: staticcheck
								{
									Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
										StartTime: startTimestamp,
										EndTime:   endTimestamp,
									},
									Value: &monitoringpb.TypedValue{ //nolint: staticcheck
										Value: &monitoringpb.TypedValue_Int64Value{
											Int64Value: 1,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for i, tt := range tests {
		se := tt.statsExporter
		if se == nil {
			se = new(statsExporter)
		}
		allTss, err := protoMetricToTimeSeries(context.Background(), se, se.getResource(nil, tt.in, seenResources), tt.in)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("#%d: unmatched error. Got\n\t%v\nWant\n\t%v", i, err, tt.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("#%d: unexpected error: %v", i, err)
			continue
		}

		got := se.combineTimeSeriesToCreateTimeSeriesRequest(allTss)
		// Our saving grace is serialization equality since some
		// unexported fields could be present in the various values.
		if diff := cmpTSReqs(got, tt.want); diff != "" {
			t.Fatalf("Test %d failed. Unexpected CreateTimeSeriesRequests -got +want: %s", i, diff)
		}
	}

	if len(seenResources) != 2 {
		t.Errorf("Should cache 2 resources, got %d", len(seenResources))
	}
}

func TestProtoToMonitoringMetricDescriptor(t *testing.T) {
	tests := []struct {
		in      *metricspb.Metric
		want    *googlemetricpb.MetricDescriptor
		wantErr string

		statsExporter *statsExporter
	}{
		{in: nil, wantErr: "non-nil metric or metric descriptor"},
		{
			in:      &metricspb.Metric{},
			wantErr: "non-nil metric or metric descriptor",
		},
		{
			in: &metricspb.Metric{
				MetricDescriptor: &metricspb.MetricDescriptor{
					Name:        "with_metric_descriptor",
					Description: "This is with metric descriptor",
					Unit:        "By",
				},
			},
			statsExporter: &statsExporter{
				o: Options{ProjectID: "test"},
			},
			want: &googlemetricpb.MetricDescriptor{
				Name:        "projects/test/metricDescriptors/custom.googleapis.com/opencensus/with_metric_descriptor",
				Type:        "custom.googleapis.com/opencensus/with_metric_descriptor",
				Labels:      []*labelpb.LabelDescriptor{},
				DisplayName: "OpenCensus/with_metric_descriptor",
				Description: "This is with metric descriptor",
				Unit:        "By",
			},
		},
		{
			in: &metricspb.Metric{
				MetricDescriptor: &metricspb.MetricDescriptor{
					Name:        "external.googleapis.com/user/with_domain",
					Description: "With metric descriptor and domain prefix",
					Unit:        "By",
				},
			},
			statsExporter: &statsExporter{
				o: Options{ProjectID: "test"},
			},
			want: &googlemetricpb.MetricDescriptor{
				Name:        "projects/test/metricDescriptors/external.googleapis.com/user/with_domain",
				Type:        "external.googleapis.com/user/with_domain",
				Labels:      []*labelpb.LabelDescriptor{},
				DisplayName: "external.googleapis.com/user/with_domain",
				Description: "With metric descriptor and domain prefix",
				Unit:        "By",
			},
		},
	}

	for i, tt := range tests {
		se := tt.statsExporter
		if se == nil {
			se = new(statsExporter)
		}
		got, err := se.protoToMonitoringMetricDescriptor(tt.in, nil)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("#%d: \nGot %v\nWanted error substring %q", i, err, tt.wantErr)
			}
			continue
		}

		if err != nil {
			t.Errorf("#%d: Unexpected error: %v", i, err)
			continue
		}

		// Our saving grace is serialization equality since some
		// unexported fields could be present in the various values.
		if diff := cmpMD(got, tt.want); diff != "" {
			t.Fatalf("Test %d failed. Unexpected MetricDescriptor -got +want: %s", i, diff)
		}
	}
}

func TestProtoMetricsToMonitoringMetrics_fromProtoPoint(t *testing.T) {
	startTimestamp := &timestamp.Timestamp{
		Seconds: 1543160298,
		Nanos:   100000090,
	}
	endTimestamp := &timestamp.Timestamp{
		Seconds: 1543160298,
		Nanos:   101000090,
	}

	tests := []struct {
		in      *metricspb.Point
		want    *monitoringpb.Point //nolint: staticcheck
		wantErr string
	}{
		{
			in: &metricspb.Point{
				Timestamp: endTimestamp,
				Value: &metricspb.Point_DistributionValue{
					DistributionValue: &metricspb.DistributionValue{
						Count:                 1,
						Sum:                   11.9,
						SumOfSquaredDeviation: 0,
						Buckets: []*metricspb.DistributionValue_Bucket{
							{}, {Count: 1}, {}, {}, {},
						},
						BucketOptions: &metricspb.DistributionValue_BucketOptions{
							Type: &metricspb.DistributionValue_BucketOptions_Explicit_{
								Explicit: &metricspb.DistributionValue_BucketOptions_Explicit{
									// With zero bucket in
									Bounds: []float64{0, 10, 20, 30, 40},
								},
							},
						},
					},
				},
			},
			want: &monitoringpb.Point{ //nolint: staticcheck
				Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
					StartTime: startTimestamp,
					EndTime:   endTimestamp,
				},
				Value: &monitoringpb.TypedValue{ //nolint: staticcheck
					Value: &monitoringpb.TypedValue_DistributionValue{
						DistributionValue: &distributionpb.Distribution{
							Count:                 1,
							Mean:                  11.9,
							SumOfSquaredDeviation: 0,
							BucketCounts:          []int64{0, 1, 0, 0, 0},
							BucketOptions: &distributionpb.Distribution_BucketOptions{
								Options: &distributionpb.Distribution_BucketOptions_ExplicitBuckets{
									ExplicitBuckets: &distributionpb.Distribution_BucketOptions_Explicit{
										Bounds: []float64{0, 10, 20, 30, 40},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			in: &metricspb.Point{
				Timestamp: endTimestamp,
				Value:     &metricspb.Point_DoubleValue{DoubleValue: 50},
			},
			want: &monitoringpb.Point{ //nolint: staticcheck
				Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
					StartTime: startTimestamp,
					EndTime:   endTimestamp,
				},
				Value: &monitoringpb.TypedValue{ //nolint: staticcheck
					Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 50},
				},
			},
		},
		{
			in: &metricspb.Point{
				Timestamp: endTimestamp,
				Value:     &metricspb.Point_Int64Value{Int64Value: 17},
			},
			want: &monitoringpb.Point{ //nolint: staticcheck
				Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
					StartTime: startTimestamp,
					EndTime:   endTimestamp,
				},
				Value: &monitoringpb.TypedValue{ //nolint: staticcheck
					Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 17},
				},
			},
		},
	}

	for i, tt := range tests {
		mpt, err := fromProtoPoint(startTimestamp, tt.in)
		if tt.wantErr != "" {
			continue
		}

		if err != nil {
			t.Errorf("#%d: unexpected error: %v", i, err)
			continue
		}

		// Our saving grace is serialization equality since some
		// unexported fields could be present in the various values.
		if diff := cmpPoint(mpt, tt.want); diff != "" {
			t.Fatalf("Test %d failed. Unexpected Point -got +want: %s", i, diff)
		}
	}
}

func TestCombineTimeSeriesAndDeduplication(t *testing.T) {
	se := new(statsExporter)

	tests := []struct {
		in   []*monitoringpb.TimeSeries              //nolint: staticcheck
		want []*monitoringpb.CreateTimeSeriesRequest //nolint: staticcheck
	}{
		{
			in: []*monitoringpb.TimeSeries{ //nolint: staticcheck
				{
					Metric: &googlemetricpb.Metric{
						Type: "a/b/c",
						Labels: map[string]string{
							"k1": "v1",
						},
					},
				},
				{
					Metric: &googlemetricpb.Metric{
						Type: "a/b/c",
						Labels: map[string]string{
							"k1": "v2",
						},
					},
				},
				{
					Metric: &googlemetricpb.Metric{
						Type: "A/b/c",
					},
				},
				{
					Metric: &googlemetricpb.Metric{
						Type: "a/b/c",
						Labels: map[string]string{
							"k1": "v1",
						},
					},
				},
				{
					Metric: &googlemetricpb.Metric{
						Type: "X/Y/Z",
					},
				},
			},
			want: []*monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
				{
					Name: fmt.Sprintf("projects/%s", se.o.ProjectID),
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &googlemetricpb.Metric{
								Type: "a/b/c",
								Labels: map[string]string{
									"k1": "v1",
								},
							},
						},
						{
							Metric: &googlemetricpb.Metric{
								Type: "a/b/c",
								Labels: map[string]string{
									"k1": "v2",
								},
							},
						},
						{
							Metric: &googlemetricpb.Metric{
								Type: "A/b/c",
							},
						},
						{
							Metric: &googlemetricpb.Metric{
								Type: "X/Y/Z",
							},
						},
					},
				},
				{
					Name: fmt.Sprintf("projects/%s", se.o.ProjectID),
					TimeSeries: []*monitoringpb.TimeSeries{ //nolint: staticcheck
						{
							Metric: &googlemetricpb.Metric{
								Type: "a/b/c",
								Labels: map[string]string{
									"k1": "v1",
								},
							},
						},
					},
				},
			},
		},
	}

	for i, tt := range tests {
		got := se.combineTimeSeriesToCreateTimeSeriesRequest(tt.in)
		if diff := cmpTSReqs(got, tt.want); diff != "" {
			t.Fatalf("Test %d failed. Unexpected CreateTimeSeriesRequests -got +want: %s", i, diff)
		}
	}
}

func TestConvertSummaryMetrics(t *testing.T) {
	startTimestamp := &timestamp.Timestamp{
		Seconds: 1543160298,
		Nanos:   100000090,
	}
	endTimestamp := &timestamp.Timestamp{
		Seconds: 1543160298,
		Nanos:   101000090,
	}

	res := &resourcepb.Resource{
		Type: resourcekeys.ContainerType,
		Labels: map[string]string{
			resourcekeys.ContainerKeyName:  "container1",
			resourcekeys.K8SKeyClusterName: "cluster1",
		},
	}

	tests := []struct {
		in            *metricspb.Metric
		want          []*metricspb.Metric
		statsExporter *statsExporter
	}{
		{
			in: &metricspb.Metric{
				MetricDescriptor: &metricspb.MetricDescriptor{
					Name:        "summary_metric_descriptor",
					Description: "This is a test",
					Unit:        "ms",
					Type:        metricspb.MetricDescriptor_SUMMARY,
				},
				Timeseries: []*metricspb.TimeSeries{
					{
						StartTimestamp: startTimestamp,
						Points: []*metricspb.Point{
							{
								Timestamp: endTimestamp,
								Value: &metricspb.Point_SummaryValue{
									SummaryValue: &metricspb.SummaryValue{
										Count: &wrappers.Int64Value{Value: 10},
										Sum:   &wrappers.DoubleValue{Value: 119.0},
										Snapshot: &metricspb.SummaryValue_Snapshot{
											PercentileValues: []*metricspb.SummaryValue_Snapshot_ValueAtPercentile{
												makePercentileValue(5.6, 10.0),
												makePercentileValue(9.6, 50.0),
												makePercentileValue(12.6, 90.0),
												makePercentileValue(19.6, 99.0),
											},
										},
									},
								},
							},
						},
					},
				},
				Resource: res,
			},
			statsExporter: &statsExporter{
				o: Options{ProjectID: "foo"},
			},
			want: []*metricspb.Metric{
				{
					MetricDescriptor: &metricspb.MetricDescriptor{
						Name:        "summary_metric_descriptor_summary_sum",
						Description: "This is a test",
						Unit:        "ms",
						Type:        metricspb.MetricDescriptor_CUMULATIVE_DOUBLE,
					},
					Timeseries: []*metricspb.TimeSeries{
						makeDoubleTs(119.0, "", startTimestamp, endTimestamp),
					},
					Resource: res,
				},
				{
					MetricDescriptor: &metricspb.MetricDescriptor{
						Name:        "summary_metric_descriptor_summary_count",
						Description: "This is a test",
						Unit:        "1",
						Type:        metricspb.MetricDescriptor_CUMULATIVE_INT64,
					},
					Timeseries: []*metricspb.TimeSeries{
						makeInt64Ts(10, "", startTimestamp, endTimestamp),
					},
					Resource: res,
				},
				{
					MetricDescriptor: &metricspb.MetricDescriptor{
						Name:        "summary_metric_descriptor_summary_percentile",
						Description: "This is a test",
						Unit:        "ms",
						Type:        metricspb.MetricDescriptor_GAUGE_DOUBLE,
						LabelKeys: []*metricspb.LabelKey{
							percentileLabelKey,
						},
					},
					Timeseries: []*metricspb.TimeSeries{
						makeDoubleTs(5.6, "10.000000", nil, endTimestamp),
						makeDoubleTs(9.6, "50.000000", nil, endTimestamp),
						makeDoubleTs(12.6, "90.000000", nil, endTimestamp),
						makeDoubleTs(19.6, "99.000000", nil, endTimestamp),
					},
					Resource: res,
				},
			},
		},
	}

	for _, tt := range tests {
		se := tt.statsExporter
		if se == nil {
			se = new(statsExporter)
		}
		got := se.convertSummaryMetrics(tt.in)
		if !cmp.Equal(got, tt.want, protocmp.Transform()) {
			t.Fatalf("conversion failed:\n  got=%v\n want=%v\n", got, tt.want)
		}
	}
}

func TestMetricPrefix(t *testing.T) {
	tests := []struct {
		name          string
		in            string
		want          string
		statsExporter *statsExporter
	}{
		{
			name: "No prefix and metric name has a kubernetes domain",
			in:   "kubernetes.io/container/memory/limit_bytes",
			statsExporter: &statsExporter{
				o: Options{ProjectID: "foo"},
			},
			want: "kubernetes.io/container/memory/limit_bytes",
		},
		{
			name: "Has a prefix but prefix doesn't have a domain",
			in:   "my_metric",
			statsExporter: &statsExporter{
				o: Options{ProjectID: "foo", MetricPrefix: "prefix/"},
			},
			want: "custom.googleapis.com/opencensus/prefix/my_metric",
		},
		{
			name: "Has a prefix without `/` ending but prefix doesn't have a domain",
			in:   "my_metric",
			statsExporter: &statsExporter{
				o: Options{ProjectID: "foo", MetricPrefix: "prefix"},
			},
			want: "custom.googleapis.com/opencensus/prefix/my_metric",
		},
		{
			name: "Has a prefix and prefix has a domain",
			in:   "my_metric",
			statsExporter: &statsExporter{
				o: Options{ProjectID: "foo", MetricPrefix: "appengine.googleapis.com/"},
			},
			want: "appengine.googleapis.com/my_metric",
		},
		{
			name: "Has a GetMetricPrefix func but result doesn't have a domain",
			in:   "my_metric",
			statsExporter: &statsExporter{
				o: Options{
					ProjectID: "foo",
					GetMetricPrefix: func(name string) string {
						return "prefix"
					}},
			},
			want: "custom.googleapis.com/opencensus/prefix/my_metric",
		},
		{
			name: "Has a GetMetricPrefix func and result has a domain",
			in:   "my_metric",
			statsExporter: &statsExporter{
				o: Options{
					ProjectID: "foo",
					GetMetricPrefix: func(name string) string {
						return "knative.dev/serving"
					}},
			},
			want: "knative.dev/serving/my_metric",
		},
		{
			name: "Has both a prefix and GetMetricPrefix func",
			in:   "my_metric",
			statsExporter: &statsExporter{
				o: Options{
					ProjectID:    "foo",
					MetricPrefix: "appengine.googleapis.com/",
					GetMetricPrefix: func(name string) string {
						return "knative.dev/serving"
					}},
			},
			want: "knative.dev/serving/my_metric",
		},
	}

	for _, tt := range tests {
		got := tt.statsExporter.metricTypeFromProto(tt.in)
		if !cmp.Equal(got, tt.want) {
			t.Fatalf("mismatch metric names for test %v:\n  got=%v\n want=%v\n", tt.name, got, tt.want)
		}
	}
}

func makeInt64Ts(val int64, label string, start, end *timestamp.Timestamp) *metricspb.TimeSeries {
	ts := &metricspb.TimeSeries{
		StartTimestamp: start,
		Points:         makeInt64Point(val, end),
	}
	if label != "" {
		ts.LabelValues = makeLabelValue(label)
	}
	return ts
}

func makeInt64Point(val int64, end *timestamp.Timestamp) []*metricspb.Point {
	return []*metricspb.Point{
		{
			Timestamp: end,
			Value: &metricspb.Point_Int64Value{
				Int64Value: val,
			},
		},
	}
}

func makeDoubleTs(val float64, label string, start, end *timestamp.Timestamp) *metricspb.TimeSeries {
	ts := &metricspb.TimeSeries{
		StartTimestamp: start,
		Points:         makeDoublePoint(val, end),
	}
	if label != "" {
		ts.LabelValues = makeLabelValue(label)
	}
	return ts
}

func makeDoublePoint(val float64, end *timestamp.Timestamp) []*metricspb.Point {
	return []*metricspb.Point{
		{
			Timestamp: end,
			Value: &metricspb.Point_DoubleValue{
				DoubleValue: val,
			},
		},
	}
}

func makeLabelValue(value string) []*metricspb.LabelValue {
	return []*metricspb.LabelValue{
		{
			HasValue: true,
			Value:    value,
		},
	}
}

func makePercentileValue(val, percentile float64) *metricspb.SummaryValue_Snapshot_ValueAtPercentile {
	return &metricspb.SummaryValue_Snapshot_ValueAtPercentile{
		Value:      val,
		Percentile: percentile,
	}
}

func protoMetricToTimeSeries(ctx context.Context, se *statsExporter, mappedRsc *monitoredrespb.MonitoredResource, metric *metricspb.Metric) ([]*monitoringpb.TimeSeries, error) { //nolint: staticcheck
	mb := newMetricsBatcher(ctx, se.o.ProjectID, se.o.NumberOfWorkers, se.c, defaultTimeout)
	se.protoMetricToTimeSeries(ctx, mappedRsc, metric, mb)
	return mb.allTss, mb.close(ctx)
}
