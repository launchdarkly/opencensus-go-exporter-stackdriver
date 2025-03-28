// Copyright 2019, OpenCensus Authors
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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	monitoringpb "google.golang.org/genproto/googleapis/monitoring/v3" //nolint: staticcheck
)

const (
	minNumWorkers   = 1
	minReqsChanSize = 5
)

type metricsBatcher struct {
	projectName string
	allTss      []*monitoringpb.TimeSeries //nolint: staticcheck
	allErrs     []error

	// Counts all dropped TimeSeries by this metricsBatcher.
	droppedTimeSeries int

	workers []*worker
	// reqsChan, respsChan and wg are shared between metricsBatcher and worker goroutines.
	reqsChan  chan *monitoringpb.CreateTimeSeriesRequest //nolint: staticcheck
	respsChan chan *response
	wg        *sync.WaitGroup
}

func newMetricsBatcher(ctx context.Context, projectID string, numWorkers int, mc *monitoring.MetricClient, timeout time.Duration) *metricsBatcher {
	if numWorkers < minNumWorkers {
		numWorkers = minNumWorkers
	}
	workers := make([]*worker, 0, numWorkers)
	reqsChanSize := numWorkers
	if reqsChanSize < minReqsChanSize {
		reqsChanSize = minReqsChanSize
	}
	reqsChan := make(chan *monitoringpb.CreateTimeSeriesRequest, reqsChanSize) //nolint: staticcheck
	respsChan := make(chan *response, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		w := newWorker(ctx, mc, reqsChan, respsChan, &wg, timeout)
		workers = append(workers, w)
		go w.start()
	}
	return &metricsBatcher{
		projectName:       fmt.Sprintf("projects/%s", projectID),
		allTss:            make([]*monitoringpb.TimeSeries, 0, maxTimeSeriesPerUpload), //nolint: staticcheck
		droppedTimeSeries: 0,
		workers:           workers,
		wg:                &wg,
		reqsChan:          reqsChan,
		respsChan:         respsChan,
	}
}

func (mb *metricsBatcher) recordDroppedTimeseries(numTimeSeries int, errs ...error) {
	mb.droppedTimeSeries += numTimeSeries
	for _, err := range errs {
		if err != nil {
			mb.allErrs = append(mb.allErrs, err)
		}
	}
}

func (mb *metricsBatcher) addTimeSeries(ts *monitoringpb.TimeSeries) { //nolint: staticcheck
	mb.allTss = append(mb.allTss, ts)
	if len(mb.allTss) == maxTimeSeriesPerUpload {
		mb.sendReqToChan()
		mb.allTss = make([]*monitoringpb.TimeSeries, 0, maxTimeSeriesPerUpload) //nolint: staticcheck
	}
}

func (mb *metricsBatcher) close(ctx context.Context) error {
	// Send any remaining time series, must be <200
	if len(mb.allTss) > 0 {
		mb.sendReqToChan()
	}

	close(mb.reqsChan)
	mb.wg.Wait()
	for i := 0; i < len(mb.workers); i++ {
		resp := <-mb.respsChan
		mb.recordDroppedTimeseries(resp.droppedTimeSeries, resp.errs...)
	}
	close(mb.respsChan)

	numErrors := len(mb.allErrs)
	if numErrors == 0 {
		return nil
	}

	if numErrors == 1 {
		return mb.allErrs[0]
	}

	errMsgs := make([]string, 0, numErrors)
	for _, err := range mb.allErrs {
		errMsgs = append(errMsgs, err.Error())
	}
	return fmt.Errorf("[%s]", strings.Join(errMsgs, "; "))
}

// sendReqToChan grabs all the timeseies in this metricsBatcher, puts them
// to a CreateTimeSeriesRequest and sends the request to reqsChan.
func (mb *metricsBatcher) sendReqToChan() {
	req := &monitoringpb.CreateTimeSeriesRequest{ //nolint: staticcheck
		Name:       mb.projectName,
		TimeSeries: mb.allTss,
	}
	mb.reqsChan <- req
}

// regex to extract min-max ranges from error response strings in the format "timeSeries[(min-max,...)] ..." (max is optional)
var timeSeriesErrRegex = regexp.MustCompile(`: timeSeries\[([0-9]+(?:-[0-9]+)?(?:,[0-9]+(?:-[0-9]+)?)*)\]`)

// sendReq sends create time series requests to Stackdriver,
// and returns the count of dropped time series and error.
func sendReq(ctx context.Context, c *monitoring.MetricClient, req *monitoringpb.CreateTimeSeriesRequest) (int, []error) { //nolint: staticcheck
	// c == nil only happens in unit tests where we don't make real calls to Stackdriver server
	if c == nil {
		return 0, nil
	}

	dropped := 0
	errors := []error{}
	serviceReq, nonServiceReq := splitCreateTimeSeriesRequest(req)
	if nonServiceReq != nil {
		err := createTimeSeries(ctx, c, nonServiceReq)
		if err != nil {
			dropped += droppedTimeSeriesFromMonitoringAPIError(nonServiceReq, err)
			errors = append(errors, err)
		}
	}
	if serviceReq != nil {
		err := createServiceTimeSeries(ctx, c, serviceReq)
		if err != nil {
			dropped += droppedTimeSeriesFromMonitoringAPIError(serviceReq, err)
			errors = append(errors, err)
		}
	}
	return dropped, errors
}

func droppedTimeSeriesFromMonitoringAPIError(req *monitoringpb.CreateTimeSeriesRequest, monitoringAPIerr error) int { //nolint: staticcheck
	droppedTimeSeriesRangeMatches := timeSeriesErrRegex.FindAllStringSubmatch(monitoringAPIerr.Error(), -1)
	if !strings.HasPrefix(monitoringAPIerr.Error(), "One or more TimeSeries could not be written:") || len(droppedTimeSeriesRangeMatches) == 0 {
		return len(req.TimeSeries)
	}

	dropped := 0
	for _, submatches := range droppedTimeSeriesRangeMatches {
		for i := 1; i < len(submatches); i++ {
			for _, rng := range strings.Split(submatches[i], ",") {
				rngSlice := strings.Split(rng, "-")

				// strconv errors not possible due to regex above
				min, _ := strconv.Atoi(rngSlice[0])
				max := min
				if len(rngSlice) > 1 {
					max, _ = strconv.Atoi(rngSlice[1])
				}

				dropped += max - min + 1
			}
		}
	}
	return dropped
}

type worker struct {
	ctx     context.Context
	timeout time.Duration
	mc      *monitoring.MetricClient

	resp *response

	respsChan chan *response
	reqsChan  chan *monitoringpb.CreateTimeSeriesRequest //nolint: staticcheck

	wg *sync.WaitGroup
}

func newWorker(
	ctx context.Context,
	mc *monitoring.MetricClient,
	reqsChan chan *monitoringpb.CreateTimeSeriesRequest, //nolint: staticcheck
	respsChan chan *response,
	wg *sync.WaitGroup,
	timeout time.Duration) *worker {
	return &worker{
		ctx:       ctx,
		mc:        mc,
		resp:      &response{},
		reqsChan:  reqsChan,
		respsChan: respsChan,
		wg:        wg,
	}
}

func (w *worker) start() {
	for req := range w.reqsChan {
		w.sendReqWithTimeout(req)
	}
	w.respsChan <- w.resp
	w.wg.Done()
}

func (w *worker) sendReqWithTimeout(req *monitoringpb.CreateTimeSeriesRequest) { //nolint: staticcheck
	ctx, cancel := newContextWithTimeout(w.ctx, w.timeout)
	defer cancel()

	w.recordDroppedTimeseries(sendReq(ctx, w.mc, req))
}

func (w *worker) recordDroppedTimeseries(numTimeSeries int, errors []error) {
	w.resp.droppedTimeSeries += numTimeSeries
	if len(errors) > 0 {
		w.resp.errs = append(w.resp.errs, errors...)
	}
}

type response struct {
	droppedTimeSeries int
	errs              []error
}
