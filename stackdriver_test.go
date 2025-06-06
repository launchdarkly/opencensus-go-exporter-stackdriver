// Copyright 2020, OpenCensus Authors
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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/launchdarkly/opencensus-go-exporter-stackdriver/internal/testpb"
	"github.com/launchdarkly/opencensus-go-exporter-stackdriver/monitoredresource/gcp"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/trace"
	"golang.org/x/net/context/ctxhttp"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	dummyAutodetect = func() gcp.Interface {
		return nil
	}
)

func init() {
	// monitoredresource.Autodetect() takes a few seconds to return when
	// run outside of a cloud environment, so use a dummy autodetect for tests
	autodetectFunc = dummyAutodetect
}

func TestExport(t *testing.T) {
	projectID, ok := os.LookupEnv("STACKDRIVER_TEST_PROJECT_ID")
	if !ok {
		t.Skip("STACKDRIVER_TEST_PROJECT_ID not set")
	}

	var exportErrors []error

	exporter, err := NewExporter(Options{ProjectID: projectID, OnError: func(err error) {
		exportErrors = append(exportErrors, err)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer exporter.Flush()

	trace.RegisterExporter(exporter)
	defer trace.UnregisterExporter(exporter)
	view.RegisterExporter(exporter)
	defer view.UnregisterExporter(exporter)

	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})

	_, span := trace.StartSpan(context.Background(), "custom-span")
	time.Sleep(10 * time.Millisecond)
	span.End()

	// Test HTTP spans

	handler := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		_, backgroundSpan := trace.StartSpan(context.Background(), "BackgroundWork")
		spanContext := backgroundSpan.SpanContext()
		time.Sleep(10 * time.Millisecond)
		backgroundSpan.End()

		_, span := trace.StartSpan(req.Context(), "Sleep")
		span.AddLink(trace.Link{Type: trace.LinkTypeChild, TraceID: spanContext.TraceID, SpanID: spanContext.SpanID})
		time.Sleep(150 * time.Millisecond) // do work
		span.End()
		rw.Write([]byte("Hello, world!")) //nolint: errcheck
	})
	server := httptest.NewServer(&ochttp.Handler{Handler: handler})
	defer server.Close()

	ctx := context.Background()
	client := &http.Client{
		Transport: &ochttp.Transport{},
	}
	resp, err := ctxhttp.Get(ctx, client, server.URL+"/test/123?abc=xyz")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if want, got := "Hello, world!", string(body); want != got {
		t.Fatalf("resp.Body = %q; want %q", want, got)
	}

	// Flush twice to expose issue of exporter creating traces internally (#557)
	exporter.Flush()
	exporter.Flush()

	for _, err := range exportErrors {
		t.Error(err)
	}
}

func TestGRPC(t *testing.T) {
	projectID, ok := os.LookupEnv("STACKDRIVER_TEST_PROJECT_ID")
	if !ok {
		t.Skip("STACKDRIVER_TEST_PROJECT_ID not set")
	}

	exporter, err := NewExporter(Options{ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	defer exporter.Flush()

	trace.RegisterExporter(exporter)
	defer trace.UnregisterExporter(exporter)
	view.RegisterExporter(exporter)
	defer view.UnregisterExporter(exporter)

	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})

	client, done := testpb.NewTestClient(t)
	defer done()

	client.Single(context.Background(), &testpb.FooRequest{SleepNanos: int64(42 * time.Millisecond)}) //nolint: errcheck
}

func TestUserAgent(t *testing.T) {
	e, err := NewExporter(Options{UserAgent: "OpenCensus Service"})
	if err != nil {
		t.Fatal(err)
	}

	if want, got := "OpenCensus Service", e.statsExporter.o.UserAgent; want != got {
		t.Fatalf("UserAgent = %q; want %q", got, want)
	}
}

func TestClose(t *testing.T) {
	projectID, ok := os.LookupEnv("STACKDRIVER_TEST_PROJECT_ID")
	if !ok {
		t.Skip("STACKDRIVER_TEST_PROJECT_ID not set")
	}

	conn, err := grpc.Dial("monitoring.googleapis.com:443", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("cannot configure grpc conn: %v", err)
	}
	copts := []option.ClientOption{option.WithGRPCConn(conn)}

	// option.WithGRPCConn option takes precedent over all other supplied options so the
	// following user agent will be used by both exporters
	exporter, err := NewExporter(Options{
		ProjectID:               projectID,
		MonitoringClientOptions: copts,
		TraceClientOptions:      copts,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = exporter.Close()
	if err != nil {
		t.Fatal(err)
	}
}
