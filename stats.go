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
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"go.opencensus.io/trace"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"github.com/golang/protobuf/ptypes/timestamp"
	"go.opencensus.io/metric/metricdata"
	"go.opencensus.io/metric/metricexport"
	"google.golang.org/api/option"
	"google.golang.org/api/support/bundler"
	distributionpb "google.golang.org/genproto/googleapis/api/distribution"
	labelpb "google.golang.org/genproto/googleapis/api/label"
	metricpb "google.golang.org/genproto/googleapis/api/metric"
	monitoredrespb "google.golang.org/genproto/googleapis/api/monitoredres"
	monitoringpb "google.golang.org/genproto/googleapis/monitoring/v3" //nolint: staticcheck
	"google.golang.org/protobuf/proto"
)

const (
	maxTimeSeriesPerUpload    = 200
	opencensusTaskKey         = "opencensus_task"
	opencensusTaskDescription = "Opencensus task identifier"
	defaultDisplayNamePrefix  = "OpenCensus"
	version                   = "0.13.3"
)

// statsExporter exports stats to the Stackdriver Monitoring.
type statsExporter struct {
	o Options

	viewDataBundler *bundler.Bundler
	metricsBundler  *bundler.Bundler

	protoMu                sync.Mutex
	protoMetricDescriptors map[string]bool // Metric descriptors that were already created remotely

	metricMu          sync.Mutex
	metricDescriptors map[string]bool // Metric descriptors that were already created remotely

	c             *monitoring.MetricClient
	defaultLabels map[string]labelValue
	ir            *metricexport.IntervalReader

	initReaderOnce sync.Once
}

var (
	errBlankProjectID = errors.New("expecting a non-blank ProjectID")
)

// newStatsExporter returns an exporter that uploads stats data to Stackdriver Monitoring.
// Only one Stackdriver exporter should be created per ProjectID per process, any subsequent
// invocations of NewExporter with the same ProjectID will return an error.
func newStatsExporter(o Options) (*statsExporter, error) {
	if strings.TrimSpace(o.ProjectID) == "" {
		return nil, errBlankProjectID
	}

	opts := append(o.MonitoringClientOptions, option.WithUserAgent(o.UserAgent))
	ctx := o.Context
	if ctx == nil {
		ctx = context.Background()
	}
	client, err := monitoring.NewMetricClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	e := &statsExporter{
		c:                      client,
		o:                      o,
		protoMetricDescriptors: make(map[string]bool),
		metricDescriptors:      make(map[string]bool),
	}

	var defaultLablesNotSanitized map[string]labelValue
	if o.DefaultMonitoringLabels != nil {
		defaultLablesNotSanitized = o.DefaultMonitoringLabels.m
	} else {
		defaultLablesNotSanitized = map[string]labelValue{
			opencensusTaskKey: {val: getTaskValue(), desc: opencensusTaskDescription},
		}
	}

	e.defaultLabels = make(map[string]labelValue)
	// Fill in the defaults firstly, irrespective of if the labelKeys and labelValues are mismatched.
	for key, label := range defaultLablesNotSanitized {
		e.defaultLabels[sanitize(key)] = label
	}

	e.viewDataBundler = bundler.NewBundler((*view.Data)(nil), func(bundle interface{}) {
		vds := bundle.([]*view.Data)
		e.handleUpload(vds...)
	})
	e.metricsBundler = bundler.NewBundler((*metricdata.Metric)(nil), func(bundle interface{}) {
		metrics := bundle.([]*metricdata.Metric)
		e.handleMetricsUpload(metrics)
	})
	if delayThreshold := e.o.BundleDelayThreshold; delayThreshold > 0 {
		e.viewDataBundler.DelayThreshold = delayThreshold
		e.metricsBundler.DelayThreshold = delayThreshold
	}
	if countThreshold := e.o.BundleCountThreshold; countThreshold > 0 {
		e.viewDataBundler.BundleCountThreshold = countThreshold
		e.metricsBundler.BundleCountThreshold = countThreshold
	}
	return e, nil
}

func (e *statsExporter) startMetricsReader() error {
	e.initReaderOnce.Do(func() {
		e.ir, _ = metricexport.NewIntervalReader(metricexport.NewReader(), e)
	})
	e.ir.ReportingInterval = e.o.ReportingInterval
	return e.ir.Start()
}

func (e *statsExporter) stopMetricsReader() {
	if e.ir != nil {
		e.ir.Stop()
		e.ir.Flush()
	}
}

func (e *statsExporter) close() error {
	return e.c.Close()
}

func (e *statsExporter) getMonitoredResource(v *view.View, tags []tag.Tag) ([]tag.Tag, *monitoredrespb.MonitoredResource) {
	resource := e.o.Resource
	if resource == nil {
		resource = &monitoredrespb.MonitoredResource{
			Type: "global",
		}
	}
	return tags, resource
}

// ExportView exports to the Stackdriver Monitoring if view data
// has one or more rows.
func (e *statsExporter) ExportView(vd *view.Data) {
	if len(vd.Rows) == 0 {
		return
	}
	err := e.viewDataBundler.Add(vd, 1)
	switch err {
	case nil:
		return
	case bundler.ErrOverflow:
		e.o.handleError(errors.New("failed to upload: buffer full"))
	default:
		e.o.handleError(err)
	}
}

// getTaskValue returns a task label value in the format of
// "go-<pid>@<hostname>".
func getTaskValue() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost"
	}
	return "go-" + strconv.Itoa(os.Getpid()) + "@" + hostname
}

// handleUpload handles uploading a slice
// of Data, as well as error handling.
func (e *statsExporter) handleUpload(vds ...*view.Data) {
	if err := e.uploadStats(vds); err != nil {
		e.o.handleError(err)
	}
}

// Flush waits for exported view data and metrics to be uploaded.
//
// This is useful if your program is ending and you do not
// want to lose data that hasn't yet been exported.
func (e *statsExporter) Flush() {
	e.viewDataBundler.Flush()
	e.metricsBundler.Flush()
}

func (e *statsExporter) uploadStats(vds []*view.Data) error {
	ctx, cancel := newContextWithTimeout(e.o.Context, e.o.Timeout)
	defer cancel()
	ctx, span := trace.StartSpan(
		ctx,
		"github.com/launchdarkly/opencensus-go-exporter-stackdriver.uploadStats",
		trace.WithSampler(trace.NeverSample()),
	)
	defer span.End()

	for _, vd := range vds {
		if err := e.createMetricDescriptorFromView(ctx, vd.View); err != nil {
			span.SetStatus(trace.Status{Code: 2, Message: err.Error()})
			return err
		}
	}
	for _, req := range e.makeReq(vds, maxTimeSeriesPerUpload) {
		if err := createTimeSeries(ctx, e.c, req); err != nil {
			span.SetStatus(trace.Status{Code: 2, Message: err.Error()})
			// TODO(jbd): Don't fail fast here, batch errors?
			return err
		}
	}
	return nil
}

func (e *statsExporter) makeReq(vds []*view.Data, limit int) []*monitoringpb.CreateTimeSeriesRequest { //nolint: staticcheck
	var reqs []*monitoringpb.CreateTimeSeriesRequest //nolint: staticcheck

	var allTimeSeries []*monitoringpb.TimeSeries //nolint: staticcheck
	for _, vd := range vds {
		for _, row := range vd.Rows {
			tags, resource := e.getMonitoredResource(vd.View, append([]tag.Tag(nil), row.Tags...))
			ts := &monitoringpb.TimeSeries{ //nolint: staticcheck
				Metric: &metricpb.Metric{
					Type:   e.metricType(vd.View),
					Labels: newLabels(e.defaultLabels, tags),
				},
				Resource: resource,
				Points:   []*monitoringpb.Point{newPoint(vd.View, row, vd.Start, vd.End)}, //nolint: staticcheck
			}
			allTimeSeries = append(allTimeSeries, ts)
		}
	}

	var timeSeries []*monitoringpb.TimeSeries //nolint: staticcheck
	for _, ts := range allTimeSeries {
		timeSeries = append(timeSeries, ts)
		if len(timeSeries) == limit {
			ctsreql := e.combineTimeSeriesToCreateTimeSeriesRequest(timeSeries)
			reqs = append(reqs, ctsreql...)
			timeSeries = timeSeries[:0]
		}
	}

	if len(timeSeries) > 0 {
		ctsreql := e.combineTimeSeriesToCreateTimeSeriesRequest(timeSeries)
		reqs = append(reqs, ctsreql...)
	}
	return reqs
}

func (e *statsExporter) viewToMetricDescriptor(ctx context.Context, v *view.View) (*metricpb.MetricDescriptor, error) {
	m := v.Measure
	agg := v.Aggregation
	viewName := v.Name

	metricType := e.metricType(v)
	var valueType metricpb.MetricDescriptor_ValueType
	unit := m.Unit()
	// Default metric Kind
	metricKind := metricpb.MetricDescriptor_CUMULATIVE

	switch agg.Type {
	case view.AggTypeCount:
		valueType = metricpb.MetricDescriptor_INT64
		// If the aggregation type is count, which counts the number of recorded measurements, the unit must be "1",
		// because this view does not apply to the recorded values.
		unit = stats.UnitDimensionless
	case view.AggTypeSum:
		switch m.(type) {
		case *stats.Int64Measure:
			valueType = metricpb.MetricDescriptor_INT64
		case *stats.Float64Measure:
			valueType = metricpb.MetricDescriptor_DOUBLE
		}
	case view.AggTypeDistribution:
		valueType = metricpb.MetricDescriptor_DISTRIBUTION
	case view.AggTypeLastValue:
		metricKind = metricpb.MetricDescriptor_GAUGE
		switch m.(type) {
		case *stats.Int64Measure:
			valueType = metricpb.MetricDescriptor_INT64
		case *stats.Float64Measure:
			valueType = metricpb.MetricDescriptor_DOUBLE
		}
	default:
		return nil, fmt.Errorf("unsupported aggregation type: %s", agg.Type.String())
	}

	var displayName string
	if e.o.GetMetricDisplayName == nil {
		displayName = e.displayName(viewName)
	} else {
		displayName = e.o.GetMetricDisplayName(v)
	}

	res := &metricpb.MetricDescriptor{
		Name:        fmt.Sprintf("projects/%s/metricDescriptors/%s", e.o.ProjectID, metricType),
		DisplayName: displayName,
		Description: v.Description,
		Unit:        unit,
		Type:        metricType,
		MetricKind:  metricKind,
		ValueType:   valueType,
		Labels:      newLabelDescriptors(e.defaultLabels, v.TagKeys),
	}
	return res, nil
}

// createMetricDescriptorFromView creates a MetricDescriptor for the given view data in Stackdriver Monitoring.
// An error will be returned if there is already a metric descriptor created with the same name
// but it has a different aggregation or keys.
func (e *statsExporter) createMetricDescriptorFromView(ctx context.Context, v *view.View) error {
	// Skip create metric descriptor if configured
	if e.o.SkipCMD {
		return nil
	}

	e.metricMu.Lock()
	defer e.metricMu.Unlock()

	viewName := v.Name

	if _, created := e.metricDescriptors[viewName]; created {
		return nil
	}

	if builtinMetric(e.metricType(v)) {
		e.metricDescriptors[viewName] = true
		return nil
	}

	inMD, err := e.viewToMetricDescriptor(ctx, v)
	if err != nil {
		return err
	}

	if err = e.createMetricDescriptor(ctx, inMD); err != nil {
		return err
	}

	// Now cache the metric descriptor
	e.metricDescriptors[viewName] = true
	return nil
}

func (e *statsExporter) displayName(suffix string) string {
	if hasDomain(suffix) {
		// If the display name suffix is already prefixed with domain, skip adding extra prefix
		return suffix
	}
	return path.Join(defaultDisplayNamePrefix, suffix)
}

func (e *statsExporter) combineTimeSeriesToCreateTimeSeriesRequest(ts []*monitoringpb.TimeSeries) (ctsreql []*monitoringpb.CreateTimeSeriesRequest) { //nolint: staticcheck
	if len(ts) == 0 {
		return nil
	}

	// Since there are scenarios in which Metrics with the same Type
	// can be bunched in the same TimeSeries, we have to ensure that
	// we create a unique CreateTimeSeriesRequest with entirely unique Metrics
	// per TimeSeries, lest we'll encounter:
	//
	//      err: rpc error: code = InvalidArgument desc = One or more TimeSeries could not be written:
	//      Field timeSeries[2] had an invalid value: Duplicate TimeSeries encountered.
	//      Only one point can be written per TimeSeries per request.: timeSeries[2]
	//
	// This scenario happens when we are using the OpenCensus Agent in which multiple metrics
	// are streamed by various client applications.
	// See https://github.com/census-ecosystem/opencensus-go-exporter-stackdriver/issues/73
	uniqueTimeSeries := make([]*monitoringpb.TimeSeries, 0, len(ts))    //nolint: staticcheck
	nonUniqueTimeSeries := make([]*monitoringpb.TimeSeries, 0, len(ts)) //nolint: staticcheck
	seenMetrics := make(map[string]struct{})

	for _, tti := range ts {
		key := metricSignature(tti.Metric)
		if _, alreadySeen := seenMetrics[key]; !alreadySeen {
			uniqueTimeSeries = append(uniqueTimeSeries, tti)
			seenMetrics[key] = struct{}{}
		} else {
			nonUniqueTimeSeries = append(nonUniqueTimeSeries, tti)
		}
	}

	// UniqueTimeSeries can be bunched up together
	// While for each nonUniqueTimeSeries, we have
	// to make a unique CreateTimeSeriesRequest.
	ctsreql = append(ctsreql, &monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
		Name:       fmt.Sprintf("projects/%s", e.o.ProjectID),
		TimeSeries: uniqueTimeSeries,
	})

	// Now recursively also combine the non-unique TimeSeries
	// that were singly added to nonUniqueTimeSeries.
	// The reason is that we need optimal combinations
	// for optimal combinations because:
	// * "a/b/c"
	// * "a/b/c"
	// * "x/y/z"
	// * "a/b/c"
	// * "x/y/z"
	// * "p/y/z"
	// * "d/y/z"
	//
	// should produce:
	//      CreateTimeSeries(uniqueTimeSeries)    :: ["a/b/c", "x/y/z", "p/y/z", "d/y/z"]
	//      CreateTimeSeries(nonUniqueTimeSeries) :: ["a/b/c"]
	//      CreateTimeSeries(nonUniqueTimeSeries) :: ["a/b/c", "x/y/z"]
	nonUniqueRequests := e.combineTimeSeriesToCreateTimeSeriesRequest(nonUniqueTimeSeries)
	ctsreql = append(ctsreql, nonUniqueRequests...)

	return ctsreql
}

// metricSignature creates a unique signature consisting of a
// metric's type and its lexicographically sorted label values
// See https://github.com/census-ecosystem/opencensus-go-exporter-stackdriver/issues/120
func metricSignature(metric *metricpb.Metric) string {
	labels := metric.GetLabels()
	labelValues := make([]string, 0, len(labels))

	for _, labelValue := range labels {
		labelValues = append(labelValues, labelValue)
	}
	sort.Strings(labelValues)
	return fmt.Sprintf("%s:%s", metric.GetType(), strings.Join(labelValues, ","))
}

func newPoint(v *view.View, row *view.Row, start, end time.Time) *monitoringpb.Point { //nolint: staticcheck
	switch v.Aggregation.Type {
	case view.AggTypeLastValue:
		return newGaugePoint(v, row, end)
	default:
		return newCumulativePoint(v, row, start, end)
	}
}

func toValidTimeIntervalpb(start, end time.Time) *monitoringpb.TimeInterval { //nolint: staticcheck
	// The end time of a new interval must be at least a millisecond after the end time of the
	// previous interval, for all non-gauge types.
	// https://cloud.google.com/monitoring/api/ref_v3/rpc/google.monitoring.v3#timeinterval
	if end.Sub(start).Milliseconds() <= 1 {
		end = start.Add(time.Millisecond)
	}
	return &monitoringpb.TimeInterval{ //nolint: staticcheck
		StartTime: &timestamp.Timestamp{
			Seconds: start.Unix(),
			Nanos:   int32(start.Nanosecond()),
		},
		EndTime: &timestamp.Timestamp{
			Seconds: end.Unix(),
			Nanos:   int32(end.Nanosecond()),
		},
	}
}

func newCumulativePoint(v *view.View, row *view.Row, start, end time.Time) *monitoringpb.Point { //nolint: staticcheck
	return &monitoringpb.Point{ //nolint: staticcheck
		Interval: toValidTimeIntervalpb(start, end),
		Value:    newTypedValue(v, row),
	}
}

func newGaugePoint(v *view.View, row *view.Row, end time.Time) *monitoringpb.Point { //nolint: staticcheck
	gaugeTime := &timestamp.Timestamp{
		Seconds: end.Unix(),
		Nanos:   int32(end.Nanosecond()),
	}
	return &monitoringpb.Point{ //nolint: staticcheck
		Interval: &monitoringpb.TimeInterval{ //nolint: staticcheck
			EndTime: gaugeTime,
		},
		Value: newTypedValue(v, row),
	}
}

func newTypedValue(vd *view.View, r *view.Row) *monitoringpb.TypedValue { //nolint: staticcheck
	switch v := r.Data.(type) {
	case *view.CountData:
		return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{ //nolint: staticcheck
			Int64Value: v.Value,
		}}
	case *view.SumData:
		switch vd.Measure.(type) {
		case *stats.Int64Measure:
			return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{ //nolint: staticcheck
				Int64Value: int64(v.Value),
			}}
		case *stats.Float64Measure:
			return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{ //nolint: staticcheck
				DoubleValue: v.Value,
			}}
		}
	case *view.DistributionData:
		insertZeroBound := shouldInsertZeroBound(vd.Aggregation.Buckets...)
		return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DistributionValue{ //nolint: staticcheck
			DistributionValue: &distributionpb.Distribution{
				Count:                 v.Count,
				Mean:                  v.Mean,
				SumOfSquaredDeviation: v.SumOfSquaredDev,
				// TODO(songya): uncomment this once Stackdriver supports min/max.
				// Range: &distributionpb.Distribution_Range{
				// 	Min: v.Min,
				// 	Max: v.Max,
				// },
				BucketOptions: &distributionpb.Distribution_BucketOptions{
					Options: &distributionpb.Distribution_BucketOptions_ExplicitBuckets{
						ExplicitBuckets: &distributionpb.Distribution_BucketOptions_Explicit{
							Bounds: addZeroBoundOnCondition(insertZeroBound, vd.Aggregation.Buckets...),
						},
					},
				},
				BucketCounts: addZeroBucketCountOnCondition(insertZeroBound, v.CountPerBucket...),
			},
		}}
	case *view.LastValueData:
		switch vd.Measure.(type) {
		case *stats.Int64Measure:
			return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{ //nolint: staticcheck
				Int64Value: int64(v.Value),
			}}
		case *stats.Float64Measure:
			return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{ //nolint: staticcheck
				DoubleValue: v.Value,
			}}
		}
	}
	return nil
}

func shouldInsertZeroBound(bounds ...float64) bool {
	if len(bounds) > 0 && bounds[0] > 0.0 {
		return true
	}
	return false
}

func addZeroBucketCountOnCondition(insert bool, counts ...int64) []int64 {
	if insert {
		return append([]int64{0}, counts...)
	}
	return counts
}

func addZeroBoundOnCondition(insert bool, bounds ...float64) []float64 {
	if insert {
		return append([]float64{0.0}, bounds...)
	}
	return bounds
}

func (e *statsExporter) metricType(v *view.View) string {
	if formatter := e.o.GetMetricType; formatter != nil {
		return formatter(v)
	}
	return path.Join("custom.googleapis.com", "opencensus", v.Name)
}

func newLabels(defaults map[string]labelValue, tags []tag.Tag) map[string]string {
	labels := make(map[string]string)
	for k, lbl := range defaults {
		labels[sanitize(k)] = lbl.val
	}
	for _, tag := range tags {
		labels[sanitize(tag.Key.Name())] = tag.Value
	}
	return labels
}

func newLabelDescriptors(defaults map[string]labelValue, keys []tag.Key) []*labelpb.LabelDescriptor {
	labelDescriptors := make([]*labelpb.LabelDescriptor, 0, len(keys)+len(defaults))
	for key, lbl := range defaults {
		labelDescriptors = append(labelDescriptors, &labelpb.LabelDescriptor{
			Key:         sanitize(key),
			Description: lbl.desc,
			ValueType:   labelpb.LabelDescriptor_STRING,
		})
	}
	for _, key := range keys {
		labelDescriptors = append(labelDescriptors, &labelpb.LabelDescriptor{
			Key:       sanitize(key.Name()),
			ValueType: labelpb.LabelDescriptor_STRING, // We only use string tags
		})
	}
	return labelDescriptors
}

func (e *statsExporter) createMetricDescriptor(ctx context.Context, md *metricpb.MetricDescriptor) error {
	ctx, cancel := newContextWithTimeout(ctx, e.o.Timeout)
	defer cancel()
	cmrdesc := &monitoringpb.CreateMetricDescriptorRequest{ //nolint: staticcheck
		Name:             fmt.Sprintf("projects/%s", e.o.ProjectID),
		MetricDescriptor: md,
	}
	_, err := createMetricDescriptor(ctx, e.c, cmrdesc)
	return err
}

var createMetricDescriptor = func(ctx context.Context, c *monitoring.MetricClient, mdr *monitoringpb.CreateMetricDescriptorRequest) (*metricpb.MetricDescriptor, error) { //nolint: staticcheck //nolint: staticcheck
	return c.CreateMetricDescriptor(ctx, mdr)
}

var createTimeSeries = func(ctx context.Context, c *monitoring.MetricClient, ts *monitoringpb.CreateTimeSeriesRequest) error { //nolint: staticcheck
	return c.CreateTimeSeries(ctx, ts)
}

var createServiceTimeSeries = func(ctx context.Context, c *monitoring.MetricClient, ts *monitoringpb.CreateTimeSeriesRequest) error { //nolint: staticcheck
	return c.CreateServiceTimeSeries(ctx, ts)
}

// splitCreateTimeSeriesRequest splits a *monitoringpb.CreateTimeSeriesRequest object into two new objects:
//   - The first object only contains service time series.
//   - The second object only contains non-service time series.
//
// A returned object may be nil if no time series is found in the original request that satisfies the rules
// above.
// All other properties of the original CreateTimeSeriesRequest object are kept in the returned objects.
func splitCreateTimeSeriesRequest(req *monitoringpb.CreateTimeSeriesRequest) (*monitoringpb.CreateTimeSeriesRequest, *monitoringpb.CreateTimeSeriesRequest) { //nolint: staticcheck
	var serviceReq, nonServiceReq *monitoringpb.CreateTimeSeriesRequest //nolint: staticcheck
	serviceTs, nonServiceTs := splitTimeSeries(req.TimeSeries)
	// reset timeseries as we just split it to avoid cloning it in the calls below
	req.TimeSeries = nil
	if len(serviceTs) > 0 {
		serviceReq = proto.Clone(req).(*monitoringpb.CreateTimeSeriesRequest) //nolint: staticcheck
		serviceReq.TimeSeries = serviceTs
	}
	if len(nonServiceTs) > 0 {
		nonServiceReq = proto.Clone(req).(*monitoringpb.CreateTimeSeriesRequest) //nolint: staticcheck
		nonServiceReq.TimeSeries = nonServiceTs
	}
	return serviceReq, nonServiceReq
}

// splitTimeSeries splits a []*monitoringpb.TimeSeries slice into two:
//   - The first slice only contains service time series
//   - The second slice only contains non-service time series
func splitTimeSeries(timeSeries []*monitoringpb.TimeSeries) ([]*monitoringpb.TimeSeries, []*monitoringpb.TimeSeries) { //nolint: staticcheck
	var serviceTs, nonServiceTs []*monitoringpb.TimeSeries //nolint: staticcheck
	for _, ts := range timeSeries {
		if serviceMetric(ts.Metric.Type) {
			serviceTs = append(serviceTs, ts)
		} else {
			nonServiceTs = append(nonServiceTs, ts)
		}
	}
	return serviceTs, nonServiceTs
}

var knownServiceMetricPrefixes = []string{
	"kubernetes.io/",
}

func serviceMetric(metricType string) bool {
	for _, knownServiceMetricPrefix := range knownServiceMetricPrefixes {
		if strings.HasPrefix(metricType, knownServiceMetricPrefix) {
			return true
		}
	}
	return false
}

var knownExternalMetricPrefixes = []string{
	"custom.googleapis.com/",
	"external.googleapis.com/",
	"workload.googleapis.com/",
}

// builtinMetric returns true if a MetricType is a heuristically known
// built-in Stackdriver metric
func builtinMetric(metricType string) bool {
	for _, knownExternalMetric := range knownExternalMetricPrefixes {
		if strings.HasPrefix(metricType, knownExternalMetric) {
			return false
		}
	}
	return true
}
