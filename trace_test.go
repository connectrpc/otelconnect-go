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
	"errors"
	"fmt"
	"github.com/bufbuild/connect-go"
	pingv1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1"
	"github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1/pingv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric/unit"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func BenchmarkStreamingServerNoOptions(t *testing.B) {
	testStreaming(t)
}

func BenchmarkStreamingServerOption(t *testing.B) {
	testStreaming(t, WithTelemetry(Server))
}

func testStreaming(t *testing.B, options ...connect.HandlerOption) {
	mux := http.NewServeMux()
	mux.Handle(pingv1connect.NewPingServiceHandler(&PingServer{}, options...))
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	connectClient := pingv1connect.NewPingServiceClient(
		server.Client(),
		server.URL,
	)
	stream := connectClient.CumSum(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < t.N; i++ {
			err := stream.Send(&pingv1.CumSumRequest{Number: 12})
			if errors.Is(err, io.EOF) {
				t.Error(err)
			} else if err != nil {
				t.Error(err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		received := 0
		for i := 0; i < t.N; i++ {
			_, err := stream.Receive()
			if errors.Is(err, io.EOF) {
				t.Error(err)
			} else if err != nil {
				t.Error(err)
			} else {
				received++
			}
		}
	}()
	wg.Wait()
}

func TestMetrics(t *testing.T) {
	t.Parallel()
	metricReader := metricsdk.NewManualReader()
	meterProvider := metricsdk.NewMeterProvider(
		metricsdk.WithReader(
			metricReader,
		),
	)
	metricInterceptor, err := newMetricsInterceptor(metricsConfig{
		interceptorType: Client,
		Provider:        meterProvider,
		Meter:           meterProvider.Meter(t.Name()),
	})

	var now time.Time
	metricInterceptor.now = func() time.Time { // spoof time.Now() so that tests can be accurately run
		now = now.Add(time.Second)
		return now
	}
	pingClient, _, _ := startServer(
		nil, /* handlerOpts */
		[]connect.ClientOption{
			connect.WithInterceptors(metricInterceptor),
		},
	)
	if _, err := pingClient.Ping(context.Background(), RequestOfSize(1, 12)); err != nil {
		t.Errorf(err.Error())
	}
	metrics, err := metricReader.Collect(context.Background())
	if err != nil {
		t.Error(err)
	}
	diff := cmp.Diff(metrics, metricdata.ResourceMetrics{
		Resource: resource.NewWithAttributes("https://opentelemetry.io/schemas/1.12.0",
			attribute.KeyValue{
				Key: "service.name",
			},
			attribute.KeyValue{
				Key:   "telemetry.sdk.language",
				Value: attribute.StringValue("go"),
			},
			attribute.KeyValue{
				Key:   "telemetry.sdk.name",
				Value: attribute.StringValue("opentelemetry"),
			},
			attribute.KeyValue{
				Key:   "telemetry.sdk.version",
				Value: attribute.StringValue("1.11.1"),
			},
		),
		ScopeMetrics: []metricdata.ScopeMetrics{
			{
				Scope: instrumentation.Scope{
					Name: "TestMetrics",
				},
				Metrics: []metricdata.Metrics{
					{
						Name: "rpc.client.duration",
						Unit: "ms",
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Count: 1,
									Sum:   float64(time.Second.Milliseconds()),
									Min:   ptr(1000.0),
									Max:   ptr(1000.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: "rpc.client.request.size",
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Count: 1,
									Sum:   16,
									Min:   ptr(16.0),
									Max:   ptr(16.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: "rpc.client.response.size",
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Count: 1,
									Sum:   16,
									Min:   ptr(16.0),
									Max:   ptr(16.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: "rpc.client.requests_per_rpc",
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Count: 1,
									Sum:   1,
									Min:   ptr(1.0),
									Max:   ptr(1.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: "rpc.client.responses_per_rpc",
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Count: 1,
									Sum:   1,
									Min:   ptr(1.0),
									Max:   ptr(1.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: "rpc.client.first_write_delay",
						Unit: "ms",
						Data: metricdata.Histogram{
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: "rpc.client.inter_receive_duration",
						Unit: "ms",
						Data: metricdata.Histogram{
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: "rpc.client.inter_send_duration",
						Unit: "ms",
						Data: metricdata.Histogram{
							Temporality: metricdata.CumulativeTemporality,
						},
					},
				},
			},
		},
	}, cmpopts.IgnoreUnexported(attribute.Set{}),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint{}, "StartTime"),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint{}, "Time"),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint{}, "Bounds"),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint{}, "BucketCounts"),
		cmpopts.IgnoreFields(metricdata.ResourceMetrics{}, "Resource"),
	)
	if diff != "" {
		t.Error(diff)
	}
}

func TestWithoutTracing(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	pingClient, _, _ := startServer(
		[]connect.HandlerOption{
			WithTelemetry(
				Client,
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
				Client,
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
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
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
				Client,
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
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Fail"),
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
				Server,
				WithTracerProvider(serverTraceProvider),
				WithFilter(func(ctx context.Context, request *Request) bool {
					return false
				}),
			),
		},
		[]connect.ClientOption{
			WithTelemetry(
				Client,
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
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
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
				Client,
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
				Client,
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
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
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
			WithTelemetry(Client, WithTracerProvider(traceProvider)),
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
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
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
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
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

func (*PingServer) CumSum(
	ctx context.Context,
	stream *connect.BidiStream[pingv1.CumSumRequest, pingv1.CumSumResponse],
) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		request, err := stream.Receive()
		if err != nil && errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return fmt.Errorf("receive request: %w", err)
		}
		if err := stream.Send(&pingv1.CumSumResponse{Sum: request.Number}); err != nil {
			return fmt.Errorf("send response: %w", err)
		}
	}
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

func ptr[T any](val T) *T {
	return &val
}
