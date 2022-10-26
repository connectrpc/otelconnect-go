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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bufbuild/connect-go"
	pingv1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/connect/ping/v1"
	"github.com/bufbuild/connect-opentelemetry-go/internal/gen/connect/ping/v1/pingv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const (
	// Type of message transmitted or received.
	RPCMessageTypeKey = attribute.Key("message.type")

	// Identifier of message transmitted or received.
	RPCMessageIDKey = attribute.Key("message.id")

	// The uncompressed size of the message transmitted or received in
	// bytes.
	RPCMessageUncompressedSizeKey = attribute.Key("message.uncompressed_size")
)

func RequestOfSize(id, dataSize int64) *connect.Request[pingv1.PingRequest] {
	body := make([]byte, dataSize)
	for i := range body {
		body[i] = byte(rand.Intn(128)) //nolint: gosec
	}
	return connect.NewRequest(&pingv1.PingRequest{Id: id, Data: body})
}

func TestWithoutTracing(t *testing.T) {
	t.Parallel()

	clientUnarySR := tracetest.NewSpanRecorder()
	clientUnaryTP := trace.NewTracerProvider(trace.WithSpanProcessor(clientUnarySR))

	pingClient, _, _ := startServer(
		WithTelemetry(
			WithoutTracing(),
			WithTracerProvider(clientUnaryTP),
		),
	)
	if _, err := pingClient.Ping(context.Background(), RequestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}
	if len(clientUnarySR.Ended()) != 0 {
		t.Error("unexpected spans recorded")
	}
}

func TestBasicFilter(t *testing.T) {
	t.Parallel()

	clientUnarySR := tracetest.NewSpanRecorder()
	clientUnaryTP := trace.NewTracerProvider(trace.WithSpanProcessor(clientUnarySR))
	pingClient, host, port := startServer(
		WithTelemetry(
			WithTracerProvider(clientUnaryTP),
			WithFilter(func(ctx context.Context, request *Request) bool {
				return false
			}),
		),
	)
	req := RequestOfSize(1, 0)
	req.Header().Set("Some-Header", "foobar")
	if _, err := pingClient.Ping(context.Background(), req); err != nil {
		t.Errorf(err.Error())
	}

	if _, err := pingClient.Ping(context.Background(), RequestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}
	if len(clientUnarySR.Ended()) != 0 {
		t.Error("unexpected spans recorded")
	}
	checkUnaryClientSpans(t, clientUnarySR.Ended(),
		[]wantTraces{
			{
				spanName: "connect.ping.v1.PingService/Ping",
				events: []trace.Event{
					{
						Name: "message",
						Attributes: []attribute.KeyValue{
							RPCMessageTypeKey.String("RECEIVED"),
							RPCMessageIDKey.Int(1),
							RPCMessageUncompressedSizeKey.Int(2),
						},
					},
					{
						Name: "message",
						Attributes: []attribute.KeyValue{
							RPCMessageTypeKey.String("SENT"),
							RPCMessageIDKey.Int(1),
							RPCMessageUncompressedSizeKey.Int(2),
						},
					},
				},
				attrs: []attribute.KeyValue{
					semconv.RPCSystemKey.String("connect"),
					semconv.RPCServiceKey.String("connect.ping.v1.PingService"),
					semconv.RPCMethodKey.String("Ping"),
					semconv.NetPeerNameKey.String(host),
					semconv.NetPeerPortKey.String(port),
					attribute.Key("rpc.connect.status_code").String("success"),
				},
			},
		})
}

func TestFilterHeader(t *testing.T) {
	t.Parallel()

	clientUnarySR := tracetest.NewSpanRecorder()
	clientUnaryTP := trace.NewTracerProvider(trace.WithSpanProcessor(clientUnarySR))

	pingClient, host, port := startServer(
		WithTelemetry(
			WithTracerProvider(clientUnaryTP),
			WithFilter(func(ctx context.Context, request *Request) bool {
				return request.Header.Get("Some-Header") == "foobar"
			}),
		),
	)
	req := RequestOfSize(1, 0)
	req.Header().Set("Some-Header", "foobar")
	if _, err := pingClient.Ping(context.Background(), req); err != nil {
		t.Errorf(err.Error())
	}

	if _, err := pingClient.Ping(context.Background(), RequestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}

	checkUnaryClientSpans(t, clientUnarySR.Ended(),
		[]wantTraces{
			{
				spanName: "connect.ping.v1.PingService/Ping",
				events: []trace.Event{
					{
						Name: "message",
						Attributes: []attribute.KeyValue{
							RPCMessageTypeKey.String("RECEIVED"),
							RPCMessageIDKey.Int(1),
							RPCMessageUncompressedSizeKey.Int(2),
						},
					},
					{
						Name: "message",
						Attributes: []attribute.KeyValue{
							RPCMessageTypeKey.String("SENT"),
							RPCMessageIDKey.Int(1),
							RPCMessageUncompressedSizeKey.Int(2),
						},
					},
				},
				attrs: []attribute.KeyValue{
					semconv.RPCSystemKey.String("connect"),
					semconv.RPCServiceKey.String("connect.ping.v1.PingService"),
					semconv.RPCMethodKey.String("Ping"),
					semconv.NetPeerNameKey.String(host),
					semconv.NetPeerPortKey.String(port),
					attribute.Key("rpc.connect.status_code").String("success"),
				},
			},
		})
}

func TestInterceptors(t *testing.T) {
	t.Parallel()
	const largeMessageSize = 1000

	clientUnarySR := tracetest.NewSpanRecorder()
	clientUnaryTP := trace.NewTracerProvider(trace.WithSpanProcessor(clientUnarySR))
	pingClient, host, port := startServer(WithTelemetry(WithTracerProvider(clientUnaryTP)))

	if _, err := pingClient.Ping(context.Background(), RequestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}
	if _, err := pingClient.Ping(context.Background(), RequestOfSize(2, largeMessageSize)); err != nil {
		t.Errorf(err.Error())
	}
	checkUnaryClientSpans(t, clientUnarySR.Ended(),
		[]wantTraces{
			{
				spanName: "connect.ping.v1.PingService/Ping",
				events: []trace.Event{
					{
						Name: "message",
						Attributes: []attribute.KeyValue{
							RPCMessageTypeKey.String("RECEIVED"),
							RPCMessageIDKey.Int(1),
							RPCMessageUncompressedSizeKey.Int(2),
						},
					},
					{
						Name: "message",
						Attributes: []attribute.KeyValue{
							RPCMessageTypeKey.String("SENT"),
							RPCMessageIDKey.Int(1),
							RPCMessageUncompressedSizeKey.Int(2),
						},
					},
				},
				attrs: []attribute.KeyValue{
					semconv.RPCSystemKey.String("connect"),
					semconv.RPCServiceKey.String("connect.ping.v1.PingService"),
					semconv.RPCMethodKey.String("Ping"),
					semconv.NetPeerNameKey.String(host),
					semconv.NetPeerPortKey.String(port),
					attribute.Key("rpc.connect.status_code").String("success"),
				},
			},
			{
				spanName: "connect.ping.v1.PingService/Ping",
				events: []trace.Event{
					{
						Name: "message",
						Attributes: []attribute.KeyValue{
							RPCMessageTypeKey.String("RECEIVED"),
							RPCMessageIDKey.Int(1),
							RPCMessageUncompressedSizeKey.Int(1005),
						},
					},
					{
						Name: "message",
						Attributes: []attribute.KeyValue{
							RPCMessageTypeKey.String("SENT"),
							RPCMessageIDKey.Int(1),
							RPCMessageUncompressedSizeKey.Int(1005),
						},
					},
				},
				attrs: []attribute.KeyValue{
					semconv.RPCSystemKey.String("connect"),
					semconv.RPCServiceKey.String("connect.ping.v1.PingService"),
					semconv.RPCMethodKey.String("Ping"),
					semconv.NetPeerNameKey.String(host),
					semconv.NetPeerPortKey.String(port), /* This gets ignored later */
					attribute.Key("rpc.connect.status_code").String("success"),
				},
			},
		})
}

func startServer(opts ...connect.HandlerOption) (pingv1connect.PingServiceClient, string, string) {
	mux := http.NewServeMux()
	mux.Handle(pingv1connect.NewPingServiceHandler(&PingServer{}, opts...))
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	pingClient := pingv1connect.NewPingServiceClient(server.Client(), server.URL)
	serverurl := strings.ReplaceAll(server.URL, "http://", "")
	spl := strings.Split(serverurl, ":")
	host, port := spl[0], spl[1]
	return pingClient, host, port
}

func (ps *PingServer) Ping(
	_ context.Context,
	req *connect.Request[pingv1.PingRequest],
) (*connect.Response[pingv1.PingResponse], error) {
	res := connect.NewResponse(&pingv1.PingResponse{
		Id:   req.Msg.Id,
		Data: req.Msg.Data,
	})
	return res, nil
}

type PingServer struct {
	pingv1connect.UnimplementedPingServiceHandler
}

type wantTraces struct {
	spanName string
	events   []trace.Event
	attrs    []attribute.KeyValue
}

func checkUnaryClientSpans(t *testing.T, spans []trace.ReadOnlySpan, want []wantTraces) {
	t.Helper()
	for i, span := range spans {
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
			diff := cmp.Diff(e.Attributes, gotEvents[i].Attributes, cmp.Comparer(func(x, y attribute.KeyValue) bool {
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
