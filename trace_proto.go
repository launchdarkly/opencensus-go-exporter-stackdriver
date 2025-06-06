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
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	timestamppb "github.com/golang/protobuf/ptypes/timestamp"
	wrapperspb "github.com/golang/protobuf/ptypes/wrappers"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/trace"
	monitoredrespb "google.golang.org/genproto/googleapis/api/monitoredres"
	tracepb "google.golang.org/genproto/googleapis/devtools/cloudtrace/v2" //nolint: staticcheck
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
)

const (
	maxAnnotationEventsPerSpan = 32
	maxMessageEventsPerSpan    = 128
	maxAttributeStringValue    = 256
	agentLabel                 = "g.co/agent"

	labelHTTPHost       = `/http/host`
	labelHTTPMethod     = `/http/method`
	labelHTTPStatusCode = `/http/status_code`
	labelHTTPPath       = `/http/path`
	labelHTTPUserAgent  = `/http/user_agent`
)

// proto returns a protocol buffer representation of a SpanData.
func protoFromSpanData(s *trace.SpanData, projectID string, mr *monitoredrespb.MonitoredResource, userAgent string) *tracepb.Span { //nolint: staticcheck
	if s == nil {
		return nil
	}

	traceIDString := s.SpanContext.TraceID.String()
	spanIDString := s.SpanContext.SpanID.String()

	name := s.Name
	switch s.SpanKind {
	case trace.SpanKindClient:
		name = "Sent." + name
	case trace.SpanKindServer:
		name = "Recv." + name
	}

	sp := &tracepb.Span{ //nolint: staticcheck
		Name:                    "projects/" + projectID + "/traces/" + traceIDString + "/spans/" + spanIDString,
		SpanId:                  spanIDString,
		DisplayName:             trunc(name, 128),
		StartTime:               timestampProto(s.StartTime),
		EndTime:                 timestampProto(s.EndTime),
		SameProcessAsParentSpan: &wrapperspb.BoolValue{Value: !s.HasRemoteParent},
	}
	if p := s.ParentSpanID; p != (trace.SpanID{}) {
		sp.ParentSpanId = p.String()
	}
	if s.Status.Code != 0 || s.Status.Message != "" {
		sp.Status = &statuspb.Status{Code: s.Status.Code, Message: s.Status.Message}
	}

	var annotations, droppedAnnotationsCount, messageEvents, droppedMessageEventsCount int
	copyAttributes(&sp.Attributes, s.Attributes)

	// Copy MonitoredResources as span Attributes
	sp.Attributes = copyMonitoredResourceAttributes(sp.Attributes, mr)

	as := s.Annotations
	for i, a := range as {
		if annotations >= maxAnnotationEventsPerSpan {
			droppedAnnotationsCount = len(as) - i
			break
		}
		annotation := &tracepb.Span_TimeEvent_Annotation{Description: trunc(a.Message, maxAttributeStringValue)} //nolint: staticcheck
		copyAttributes(&annotation.Attributes, a.Attributes)
		event := &tracepb.Span_TimeEvent{ //nolint: staticcheck
			Time:  timestampProto(a.Time),
			Value: &tracepb.Span_TimeEvent_Annotation_{Annotation: annotation},
		}
		annotations++
		if sp.TimeEvents == nil {
			sp.TimeEvents = &tracepb.Span_TimeEvents{} //nolint: staticcheck
		}
		sp.TimeEvents.TimeEvent = append(sp.TimeEvents.TimeEvent, event)
	}

	if sp.Attributes == nil {
		sp.Attributes = &tracepb.Span_Attributes{ //nolint: staticcheck
			AttributeMap: make(map[string]*tracepb.AttributeValue), //nolint: staticcheck
		}
	}

	// Only set the agent label if it is not already set. That enables the
	// OpenCensus agent/collector to set the agent label based on the library that
	// sent the span to the agent.
	//
	// We now provide a config option to set the userAgent explicitly, which is
	// used both here and in request headers when sending metric data, but have
	// retained this non-override functionality for backwards compatibility.
	if _, hasAgent := sp.Attributes.AttributeMap[agentLabel]; !hasAgent {
		sp.Attributes.AttributeMap[agentLabel] = &tracepb.AttributeValue{ //nolint: staticcheck
			Value: &tracepb.AttributeValue_StringValue{
				StringValue: trunc(userAgent, maxAttributeStringValue),
			},
		}
	}

	es := s.MessageEvents
	for i, e := range es {
		if messageEvents >= maxMessageEventsPerSpan {
			droppedMessageEventsCount = len(es) - i
			break
		}
		messageEvents++
		if sp.TimeEvents == nil {
			sp.TimeEvents = &tracepb.Span_TimeEvents{} //nolint: staticcheck
		}
		sp.TimeEvents.TimeEvent = append(sp.TimeEvents.TimeEvent, &tracepb.Span_TimeEvent{ //nolint: staticcheck
			Time: timestampProto(e.Time),
			Value: &tracepb.Span_TimeEvent_MessageEvent_{
				MessageEvent: &tracepb.Span_TimeEvent_MessageEvent{ //nolint: staticcheck
					Type:                  tracepb.Span_TimeEvent_MessageEvent_Type(e.EventType), //nolint: staticcheck
					Id:                    e.MessageID,
					UncompressedSizeBytes: e.UncompressedByteSize,
					CompressedSizeBytes:   e.CompressedByteSize,
				},
			},
		})
	}

	if droppedAnnotationsCount != 0 || droppedMessageEventsCount != 0 {
		if sp.TimeEvents == nil {
			sp.TimeEvents = &tracepb.Span_TimeEvents{} //nolint: staticcheck
		}
		sp.TimeEvents.DroppedAnnotationsCount = clip32(droppedAnnotationsCount)
		sp.TimeEvents.DroppedMessageEventsCount = clip32(droppedMessageEventsCount)
	}

	if len(s.Links) > 0 {
		sp.Links = &tracepb.Span_Links{}                            //nolint: staticcheck
		sp.Links.Link = make([]*tracepb.Span_Link, 0, len(s.Links)) //nolint: staticcheck
		for _, l := range s.Links {
			link := &tracepb.Span_Link{ //nolint: staticcheck
				TraceId: l.TraceID.String(),
				SpanId:  l.SpanID.String(),
				Type:    tracepb.Span_Link_Type(l.Type), //nolint: staticcheck
			}
			copyAttributes(&link.Attributes, l.Attributes)
			sp.Links.Link = append(sp.Links.Link, link)
		}
	}
	return sp
}

// timestampProto creates a timestamp proto for a time.Time.
func timestampProto(t time.Time) *timestamppb.Timestamp {
	return &timestamppb.Timestamp{
		Seconds: t.Unix(),
		Nanos:   int32(t.Nanosecond()),
	}
}

// copyMonitoredResourceAttributes copies proto monitoredResource to proto map field (Span_Attributes)
// it creates the map if it is nil.
func copyMonitoredResourceAttributes(out *tracepb.Span_Attributes, mr *monitoredrespb.MonitoredResource) *tracepb.Span_Attributes { //nolint: staticcheck
	if mr == nil {
		return out
	}
	if out == nil {
		out = &tracepb.Span_Attributes{} //nolint: staticcheck
	}
	if out.AttributeMap == nil {
		out.AttributeMap = make(map[string]*tracepb.AttributeValue) //nolint: staticcheck
	}
	for k, v := range mr.Labels {
		av := attributeValue(v)
		out.AttributeMap[fmt.Sprintf("g.co/r/%s/%s", mr.Type, k)] = av
	}
	return out
}

// copyAttributes copies a map of attributes to a proto map field.
// It creates the map if it is nil.
func copyAttributes(out **tracepb.Span_Attributes, in map[string]interface{}) { //nolint: staticcheck
	if len(in) == 0 {
		return
	}
	if *out == nil {
		*out = &tracepb.Span_Attributes{} //nolint: staticcheck
	}
	if (*out).AttributeMap == nil {
		(*out).AttributeMap = make(map[string]*tracepb.AttributeValue) //nolint: staticcheck
	}
	var dropped int32
	for key, value := range in {
		av := attributeValue(value)
		if av == nil {
			continue
		}
		switch key {
		case ochttp.PathAttribute:
			(*out).AttributeMap[labelHTTPPath] = av
		case ochttp.HostAttribute:
			(*out).AttributeMap[labelHTTPHost] = av
		case ochttp.MethodAttribute:
			(*out).AttributeMap[labelHTTPMethod] = av
		case ochttp.UserAgentAttribute:
			(*out).AttributeMap[labelHTTPUserAgent] = av
		case ochttp.StatusCodeAttribute:
			(*out).AttributeMap[labelHTTPStatusCode] = av
		default:
			if len(key) > 128 {
				dropped++
				continue
			}
			(*out).AttributeMap[key] = av
		}
	}
	(*out).DroppedAttributesCount = dropped
}

func attributeValue(v interface{}) *tracepb.AttributeValue { //nolint: staticcheck
	switch value := v.(type) {
	case bool:
		return &tracepb.AttributeValue{ //nolint: staticcheck
			Value: &tracepb.AttributeValue_BoolValue{BoolValue: value},
		}
	case int64:
		return &tracepb.AttributeValue{ //nolint: staticcheck
			Value: &tracepb.AttributeValue_IntValue{IntValue: value},
		}
	case float64:
		// TODO: set double value if Stackdriver Trace support it in the future.
		return &tracepb.AttributeValue{ //nolint: staticcheck
			Value: &tracepb.AttributeValue_StringValue{
				StringValue: trunc(strconv.FormatFloat(value, 'f', -1, 64),
					maxAttributeStringValue)},
		}
	case string:
		return &tracepb.AttributeValue{ //nolint: staticcheck
			Value: &tracepb.AttributeValue_StringValue{StringValue: trunc(value, maxAttributeStringValue)},
		}
	}
	return nil
}

// trunc returns a TruncatableString truncated to the given limit.
func trunc(s string, limit int) *tracepb.TruncatableString { //nolint: staticcheck
	if len(s) > limit {
		b := []byte(s[:limit])
		for {
			r, size := utf8.DecodeLastRune(b)
			if r == utf8.RuneError && size == 1 {
				b = b[:len(b)-1]
			} else {
				break
			}
		}
		return &tracepb.TruncatableString{ //nolint: staticcheck
			Value:              string(b),
			TruncatedByteCount: clip32(len(s) - len(b)),
		}
	}
	return &tracepb.TruncatableString{ //nolint: staticcheck
		Value:              s,
		TruncatedByteCount: 0,
	}
}

// clip32 clips an int to the range of an int32.
func clip32(x int) int32 {
	if x < math.MinInt32 {
		return math.MinInt32
	}
	if x > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(x)
}
