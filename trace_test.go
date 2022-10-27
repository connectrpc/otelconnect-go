// Copyright 2022 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package otelconnect

import (
	"context"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pingv1 "connectrpc.com/otelconnect/internal/gen/observability/ping/v1"
	"connectrpc.com/otelconnect/internal/gen/observability/ping/v1/pingv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
)

func TestWithoutTracing(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	pingClient, _, _ := startServer(
		[]connect.HandlerOption{
			WithTelemetry(
				WithoutTracing(),
				WithTracerProvider(traceProvider),
			),
		},
		nil, /* clientOpts */
	)
	if _, err := pingClient.Ping(context.Background(), RequestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}
	if len(spanRecorder.Ended()) != 0 {
		t.Error("unexpected spans recorded")
	}
}

func TestClientSimple(t *testing.T) {
	t.Parallel()
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	pingClient, host, port := startServer(
		nil, /* handlerOpts */
		[]connect.ClientOption{
			WithTelemetry(
				WithTracerProvider(clientTraceProvider),
			),
		},
	)
	if _, err := pingClient.Ping(context.Background(), RequestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}
	checkUnarySpans(t, []wantSpans{
		{
			spanName: "observability.ping.v1.PingService/Ping",
			events: []trace.Event{
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeKey.String("SENT"),
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeKey.String("RECEIVED"),
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
				attribute.Key("rpc.connect.status_code").String("success"),
			},
		},
	}, clientSpanRecorder.Ended())
}

func TestHandlerFailCall(t *testing.T) {
	t.Parallel()
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	pingClient, host, port := startServer(
		nil,
		[]connect.ClientOption{
			WithTelemetry(
				WithTracerProvider(clientTraceProvider),
			),
		},
	)
	_, err := pingClient.Fail(
		context.Background(),
		connect.NewRequest(&pingv1.FailRequest{Code: int32(connect.CodeInternal)}),
	)
	if err == nil {
		t.Fatal("expecting error, got nil")
	}
	checkUnarySpans(t, []wantSpans{
		{
			spanName: "observability.ping.v1.PingService/Fail",
			events: []trace.Event{
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeKey.String("SENT"),
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeKey.String("RECEIVED"),
						semconv.MessageIDKey.Int(1),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Fail"),
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
				attribute.Key("rpc.connect.status_code").String("unimplemented"),
			},
		},
	}, clientSpanRecorder.Ended())
}

func TestClientHandlerOpts(t *testing.T) {
	t.Parallel()
	serverSpanRecorder := tracetest.NewSpanRecorder()
	serverTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(serverSpanRecorder))
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	pingClient, host, port := startServer(
		[]connect.HandlerOption{
			WithTelemetry(
				WithTracerProvider(serverTraceProvider),
				WithFilter(func(ctx context.Context, request *Request) bool {
					return false
				}),
			),
		},
		[]connect.ClientOption{
			WithTelemetry(
				WithTracerProvider(clientTraceProvider),
			),
		},
	)
	if _, err := pingClient.Ping(context.Background(), RequestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}
	checkUnarySpans(t, []wantSpans{}, serverSpanRecorder.Ended())
	checkUnarySpans(t, []wantSpans{
		{
			spanName: "observability.ping.v1.PingService/Ping",
			events: []trace.Event{
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeKey.String("SENT"),
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeKey.String("RECEIVED"),
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
				attribute.Key("rpc.connect.status_code").String("success"),
			},
		},
	}, clientSpanRecorder.Ended())
}

func TestBasicFilter(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	pingClient, _, _ := startServer(
		[]connect.HandlerOption{
			WithTelemetry(
				WithTracerProvider(traceProvider),
				WithFilter(func(ctx context.Context, request *Request) bool {
					return false
				}),
			),
		},
		nil, /* clientOpts */
	)
	req := RequestOfSize(1, 0)
	req.Header().Set("Some-Header", "foobar")
	if _, err := pingClient.Ping(context.Background(), req); err != nil {
		t.Errorf(err.Error())
	}
	if len(spanRecorder.Ended()) != 0 {
		t.Error("unexpected spans recorded")
	}
	checkUnarySpans(t, []wantSpans{}, spanRecorder.Ended())
}

func TestFilterHeader(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	pingClient, host, port := startServer(
		[]connect.HandlerOption{
			WithTelemetry(
				WithTracerProvider(traceProvider),
				WithFilter(func(ctx context.Context, request *Request) bool {
					return request.Header.Get("Some-Header") == "foobar"
				}),
			),
		},
		nil, /* clientOpts */
	)
	req := RequestOfSize(1, 0)
	req.Header().Set("Some-Header", "foobar")
	if _, err := pingClient.Ping(context.Background(), req); err != nil {
		t.Errorf(err.Error())
	}
	if _, err := pingClient.Ping(context.Background(), RequestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}
	checkUnarySpans(t, []wantSpans{
		{
			spanName: "observability.ping.v1.PingService/Ping",
			events: []trace.Event{
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeKey.String("RECEIVED"),
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeKey.String("SENT"),
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
				attribute.Key("rpc.connect.status_code").String("success"),
			},
		},
	}, spanRecorder.Ended())
}

func TestInterceptors(t *testing.T) {
	t.Parallel()
	const largeMessageSize = 1000
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	pingClient, host, port := startServer(
		[]connect.HandlerOption{
			WithTelemetry(WithTracerProvider(traceProvider)),
		},
		nil, /* clientOpts */
	)
	if _, err := pingClient.Ping(context.Background(), RequestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}
	if _, err := pingClient.Ping(context.Background(), RequestOfSize(2, largeMessageSize)); err != nil {
		t.Errorf(err.Error())
	}
	checkUnarySpans(t, []wantSpans{
		{
			spanName: "observability.ping.v1.PingService/Ping",
			events: []trace.Event{
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeKey.String("RECEIVED"),
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeKey.String("SENT"),
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
				attribute.Key("rpc.connect.status_code").String("success"),
			},
		},
		{
			spanName: "observability.ping.v1.PingService/Ping",
			events: []trace.Event{
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeKey.String("RECEIVED"),
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(1005),
					},
				},
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeKey.String("SENT"),
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(1005),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
				attribute.Key("rpc.connect.status_code").String("success"),
			},
		},
	}, spanRecorder.Ended())
}

func startServer(
	handlerOpts []connect.HandlerOption,
	clientOpts []connect.ClientOption) (pingv1connect.PingServiceClient, string, string) {
	mux := http.NewServeMux()
	mux.Handle(pingv1connect.NewPingServiceHandler(&PingServer{}, handlerOpts...))
	server := httptest.NewServer(mux)
	pingClient := pingv1connect.NewPingServiceClient(server.Client(), server.URL, clientOpts...)
	host, port, _ := net.SplitHostPort(strings.ReplaceAll(server.URL, "http://", ""))
	return pingClient, host, port
}

func (*PingServer) Ping(_ context.Context, req *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
	return connect.NewResponse(&pingv1.PingResponse{
		Id:   req.Msg.Id,
		Data: req.Msg.Data,
	}), nil
}

type PingServer struct {
	pingv1connect.UnimplementedPingServiceHandler
}

type wantSpans struct {
	spanName string
	events   []trace.Event
	attrs    []attribute.KeyValue
}

func checkUnarySpans(t *testing.T, want []wantSpans, got []trace.ReadOnlySpan) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("unexpected spans: want %d spans, got %d", len(want), len(got))
	}
	for i, span := range got {
		wantEvents := want[i].events
		wantAttributes := want[i].attrs
		if span.EndTime().IsZero() {
			t.Fail()
		}
		if span.Name() != want[i].spanName {
			t.Errorf("span name not %s", want[i].spanName)
		}
		gotEvents := span.Events()
		if len(wantEvents) != len(gotEvents) {
			t.Error("event lengths do not match")
		}
		for i, e := range wantEvents {
			if e.Name != gotEvents[i].Name {
				t.Error("names do not match")
			}
			diff := cmp.Diff(e.Attributes, gotEvents[i].Attributes,
				cmp.Comparer(func(x, y attribute.KeyValue) bool {
					return x.Value == y.Value && x.Key == y.Key
				}))
			if diff != "" {
				t.Error(diff)
			}
		}
		diff := cmp.Diff(span.Attributes(), wantAttributes,
			cmpopts.IgnoreUnexported(attribute.Value{}),
			cmp.Comparer(func(x, y attribute.KeyValue) bool {
				if x.Key == semconv.NetPeerPortKey && y.Key == semconv.NetPeerPortKey {
					return true
				}
				return x.Key == y.Key && x.Value == y.Value
			},
			))
		if diff != "" {
			t.Error(diff)
		}
	}
}

func RequestOfSize(id, dataSize int64) *connect.Request[pingv1.PingRequest] {
	body := make([]byte, dataSize)
	for i := range body {
		body[i] = byte(rand.Intn(128)) //nolint: gosec
	}
	return connect.NewRequest(&pingv1.PingRequest{Id: id, Data: body})
}
