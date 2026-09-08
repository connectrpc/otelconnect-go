// Copyright 2022-2025 The Connect Authors
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
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	pingv1 "connectrpc.com/otelconnect/v2/internal/gen/observability/ping/v1"
	"connectrpc.com/otelconnect/v2/internal/gen/observability/ping/v1/pingv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/semconv/v1.43.0/rpcconv"
	traceapi "go.opentelemetry.io/otel/trace"
)

const (
	pingMethod          = "Ping"
	failMethod          = "Fail"
	pingStreamMethod    = "PingStream"
	unimplementedString = "UNIMPLEMENTED"
	dataLossString      = "DATA_LOSS"
	traceParentKey      = "Traceparent"
	rpcClientDuration   = "rpc.client.call.duration"
	rpcServerDuration   = "rpc.server.call.duration"
	rpcSystemName       = "rpc.system.name"
	rpcMethod           = "rpc.method"
	rpcStatusCode       = "rpc.response.status_code"
	errorType           = "error.type"
)

// rpc.method is the generated procedure without its leading slash.
//
//nolint:gochecknoglobals
var (
	pingProcedure       = pingv1connect.PingServicePingProcedure[1:]
	failProcedure       = pingv1connect.PingServiceFailProcedure[1:]
	pingStreamProcedure = pingv1connect.PingServicePingStreamProcedure[1:]
)

func TestStreamingMetrics(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	interceptor, err := NewServerInterceptor(WithMeterProvider(meterProvider), withSecondTicks())
	require.NoError(t, err)
	connectClient, _, _ := startServer(t,
		[]connect.ServerInterceptor{interceptor},
		nil, okayPingServer())
	stream, err := connectClient.PingStream(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{Data: []byte("Hello, otel!")}))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseSend())
	_, err = stream.Receive()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, stream.Close())
	metrics := &metricdata.ResourceMetrics{}
	require.NoError(t, metricReader.Collect(context.Background(), metrics))
	// Server metrics carry no peer attributes: the conventions define
	// network.peer.* for spans only.
	diff := cmp.Diff(expectedDurationMetrics(serverKey,
		semconv.RPCSystemNameKey.String(connectProtocol),
		semconv.RPCMethodKey.String(pingStreamProcedure),
		semconv.RPCResponseStatusCodeKey.String(statusCodeOK),
	), metrics, cmpOpts()...)
	assert.Empty(t, diff)
}

func TestStreamingMetricsClient(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	interceptor, err := NewClientInterceptor(WithMeterProvider(meterProvider), withSecondTicks())
	require.NoError(t, err)
	connectClient, host, port := startServer(t,
		nil,
		[]connect.ClientInterceptor{interceptor}, okayPingServer())
	stream, err := connectClient.PingStream(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{Data: []byte("Hello, otel!")}))
	require.NoError(t, stream.CloseSend())
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	metrics := &metricdata.ResourceMetrics{}
	require.NoError(t, metricReader.Collect(context.Background(), metrics))
	diff := cmp.Diff(expectedDurationMetrics(clientKey,
		semconv.RPCSystemNameKey.String(connectProtocol),
		semconv.RPCMethodKey.String(pingStreamProcedure),
		semconv.ServerAddressKey.String(host),
		semconv.ServerPortKey.Int(port),
		semconv.RPCResponseStatusCodeKey.String(statusCodeOK),
	), metrics, cmpOpts()...)
	assert.Empty(t, diff)
}

func TestStreamingMetricsClientFail(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	interceptor, err := NewClientInterceptor(WithMeterProvider(meterProvider), withSecondTicks())
	require.NoError(t, err)
	connectClient, host, port := startServer(t,
		nil,
		[]connect.ClientInterceptor{interceptor}, failPingServer())
	stream, err := connectClient.PingStream(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{Data: []byte("Hello, otel!")}))
	require.NoError(t, stream.CloseSend())
	_, err = stream.Receive()
	require.Error(t, err)
	require.NoError(t, stream.Close())
	metrics := &metricdata.ResourceMetrics{}
	require.NoError(t, metricReader.Collect(context.Background(), metrics))
	diff := cmp.Diff(expectedDurationMetrics(clientKey,
		semconv.RPCSystemNameKey.String(connectProtocol),
		semconv.RPCMethodKey.String(pingStreamProcedure),
		semconv.ServerAddressKey.String(host),
		semconv.ServerPortKey.Int(port),
		semconv.RPCResponseStatusCodeKey.String(dataLossString),
		semconv.ErrorTypeKey.String(dataLossString),
	), metrics, cmpOpts()...)
	assert.Empty(t, diff)
}

func TestStreamingMetricsFail(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	interceptor, err := NewServerInterceptor(WithMeterProvider(meterProvider), withSecondTicks())
	require.NoError(t, err)
	connectClient, _, _ := startServer(t,
		[]connect.ServerInterceptor{interceptor},
		nil, failPingServer())
	stream, err := connectClient.PingStream(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{Data: []byte("Hello, otel!")}))
	require.NoError(t, stream.CloseSend())
	_, err = stream.Receive()
	require.Error(t, err)
	require.NoError(t, stream.Close())
	metrics := &metricdata.ResourceMetrics{}
	require.NoError(t, metricReader.Collect(context.Background(), metrics))
	diff := cmp.Diff(expectedDurationMetrics(serverKey,
		semconv.RPCSystemNameKey.String(connectProtocol),
		semconv.RPCMethodKey.String(pingStreamProcedure),
		semconv.RPCResponseStatusCodeKey.String(dataLossString),
		semconv.ErrorTypeKey.String(dataLossString),
	), metrics, cmpOpts()...)
	assert.Empty(t, diff)
}

func TestMetrics(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	interceptor, err := NewClientInterceptor(WithMeterProvider(meterProvider), withSecondTicks())
	require.NoError(t, err)
	pingClient, host, port := startServer(t, nil, []connect.ClientInterceptor{interceptor}, okayPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 12)); err != nil {
		t.Error(err)
	}
	metrics := &metricdata.ResourceMetrics{}
	require.NoError(t, metricReader.Collect(context.Background(), metrics))
	diff := cmp.Diff(expectedDurationMetrics(clientKey,
		semconv.RPCSystemNameKey.String(connectProtocol),
		semconv.RPCMethodKey.String(pingProcedure),
		semconv.ServerAddressKey.String(host),
		semconv.ServerPortKey.Int(port),
		semconv.RPCResponseStatusCodeKey.String(statusCodeOK),
	), metrics, cmpOpts()...)
	assert.Empty(t, diff)
}

func TestDurationHistogramOptions(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	interceptor, err := NewClientInterceptor(
		WithMeterProvider(meterProvider),
		WithDurationHistogramOptions(metric.WithExplicitBucketBoundaries(1, 2, 3)),
	)
	require.NoError(t, err)
	pingClient, _, _ := startServer(t, nil, []connect.ClientInterceptor{interceptor}, okayPingServer())
	_, err = pingClient.Ping(context.Background(), requestOfSize(1, 0))
	require.NoError(t, err)
	metrics := &metricdata.ResourceMetrics{}
	require.NoError(t, metricReader.Collect(context.Background(), metrics))
	require.Len(t, metrics.ScopeMetrics, 1)
	require.Len(t, metrics.ScopeMetrics[0].Metrics, 1)
	histogram, ok := metrics.ScopeMetrics[0].Metrics[0].Data.(metricdata.Histogram[float64])
	require.True(t, ok)
	require.Len(t, histogram.DataPoints, 1)
	assert.Equal(t, []float64{1, 2, 3}, histogram.DataPoints[0].Bounds)
}

func TestWithoutMetrics(t *testing.T) {
	t.Parallel()
	metricReader := metricsdk.NewManualReader()
	meterProvider := metricsdk.NewMeterProvider(
		metricsdk.WithReader(
			metricReader,
		),
	)
	interceptor, err := NewClientInterceptor(WithMeterProvider(meterProvider), WithoutMetrics())
	require.NoError(t, err)
	pingClient, _, _ := startServer(t, nil, []connect.ClientInterceptor{interceptor}, okayPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 12)); err != nil {
		t.Error(err)
	}
	metrics := &metricdata.ResourceMetrics{}
	require.NoError(t, metricReader.Collect(context.Background(), metrics))
	if len(metrics.ScopeMetrics) != 0 {
		t.Error("metrics unexpectedly recorded")
	}
}

func TestWithoutTracing(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	interceptor, err := NewServerInterceptor(WithTracerProvider(traceProvider), WithoutTracing())
	require.NoError(t, err)
	pingClient, _, _ := startServer(t, []connect.ServerInterceptor{interceptor}, nil, okayPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 0)); err != nil {
		t.Error(err)
	}
	if len(spanRecorder.Ended()) != 0 {
		t.Error("unexpected spans recorded")
	}
}

func TestClientSimple(t *testing.T) {
	t.Parallel()
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	interceptor, err := NewClientInterceptor(WithTracerProvider(clientTraceProvider))
	require.NoError(t, err)
	pingClient, host, port := startServer(t, nil, []connect.ClientInterceptor{interceptor}, okayPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 0)); err != nil {
		t.Error(err)
	}
	require.Len(t, clientSpanRecorder.Ended(), 1)
	require.Equal(t, codes.Unset, clientSpanRecorder.Ended()[0].Status().Code)
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + pingMethod,
			attrs: []attribute.KeyValue{
				semconv.RPCSystemNameKey.String(connectProtocol),
				semconv.RPCMethodKey.String(pingProcedure),
				semconv.ServerAddressKey.String(host),
				semconv.ServerPortKey.Int(port),
				semconv.RPCResponseStatusCodeKey.String(statusCodeOK),
			},
		},
	}, clientSpanRecorder.Ended())
}

func TestHandlerFailCall(t *testing.T) {
	t.Parallel()
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	interceptor, err := NewClientInterceptor(WithTracerProvider(clientTraceProvider))
	require.NoError(t, err)
	pingClient, host, port := startServer(t, nil, []connect.ClientInterceptor{interceptor}, okayPingServer())
	_, err = pingClient.Fail(context.Background(), &pingv1.FailRequest{Code: int32(connect.CodeInternal)})
	require.Error(t, err)
	require.Len(t, clientSpanRecorder.Ended(), 1)
	require.Equal(t, codes.Error, clientSpanRecorder.Ended()[0].Status().Code)
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + failMethod,
			attrs: []attribute.KeyValue{
				semconv.RPCSystemNameKey.String(connectProtocol),
				semconv.RPCMethodKey.String(failProcedure),
				semconv.ServerAddressKey.String(host),
				semconv.ServerPortKey.Int(port),
				semconv.RPCResponseStatusCodeKey.String(unimplementedString),
				semconv.ErrorTypeKey.String(unimplementedString),
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
	serverInterceptor, err := NewServerInterceptor(
		WithTracerProvider(serverTraceProvider),
		WithFilter(func(_ context.Context, _ connect.Spec) bool {
			return false
		}),
	)
	require.NoError(t, err)
	clientInterceptor, err := NewClientInterceptor(
		WithTracerProvider(clientTraceProvider),
	)
	require.NoError(t, err)
	pingClient, host, port := startServer(t, []connect.ServerInterceptor{serverInterceptor}, []connect.ClientInterceptor{clientInterceptor}, okayPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 0)); err != nil {
		t.Error(err)
	}
	assertSpans(t, []wantSpans{}, serverSpanRecorder.Ended())
	require.Len(t, clientSpanRecorder.Ended(), 1)
	require.Equal(t, codes.Unset, clientSpanRecorder.Ended()[0].Status().Code)
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + pingMethod,
			attrs: []attribute.KeyValue{
				semconv.RPCSystemNameKey.String(connectProtocol),
				semconv.RPCMethodKey.String(pingProcedure),
				semconv.ServerAddressKey.String(host),
				semconv.ServerPortKey.Int(port),
				semconv.RPCResponseStatusCodeKey.String(statusCodeOK),
			},
		},
	}, clientSpanRecorder.Ended())
}

func TestBasicFilter(t *testing.T) {
	t.Parallel()
	headerKey, headerVal := "Some-Header", "foobar"
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	metricReader, meterProvider := setupMetrics()
	serverInterceptor, err := NewServerInterceptor(
		WithTracerProvider(traceProvider),
		WithMeterProvider(meterProvider),
		WithFilter(func(_ context.Context, _ connect.Spec) bool {
			return false
		}))
	require.NoError(t, err)
	pingClient, _, _ := startServer(t, []connect.ServerInterceptor{serverInterceptor}, nil, okayPingServer())
	ctx := withRequestHeader(context.Background(), headerKey, headerVal)
	if _, err := pingClient.Ping(ctx, requestOfSize(1, 0)); err != nil {
		t.Error(err)
	}
	if len(spanRecorder.Ended()) != 0 {
		t.Error("unexpected spans recorded")
	}
	assertSpans(t, []wantSpans{}, spanRecorder.Ended())

	// Verify no metrics are recorded when filtered out
	metrics := &metricdata.ResourceMetrics{}
	require.NoError(t, metricReader.Collect(context.Background(), metrics))

	// Should have no scope metrics when filtered out
	assert.Empty(t, metrics.ScopeMetrics, "No metrics should be recorded when filtered out")
}

func TestHeaderAttribute(t *testing.T) {
	t.Parallel()
	var propagator propagation.TraceContext
	handlerSpanRecorder := tracetest.NewSpanRecorder()
	handlerTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(handlerSpanRecorder))
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	pingReq, pingRes, cumsumReq, cumsumRes := "pingReq", "pingRes", "cumsumReq", "cumsumRes"
	pingReqKey := "rpc.request.metadata.pingreq"
	pingResKey := "rpc.response.metadata.pingres"
	cumsumReqKey := "rpc.request.metadata.cumsumreq"
	cumsumResKey := "rpc.response.metadata.cumsumres"
	value := "value"
	attributeValue := []string{value}
	attributeValueLong := []string{value, value}
	attributePingReq := attribute.StringSlice(pingReqKey, attributeValue)
	attributeCumsumReq := attribute.StringSlice(cumsumReqKey, attributeValue)
	attributePingRes := attribute.StringSlice(pingResKey, attributeValueLong)
	attributeCumsumRes := attribute.StringSlice(cumsumResKey, attributeValue)
	requestHeaderOption := WithTraceRequestHeader(pingReq, cumsumReq)
	responseHeaderOption := WithTraceResponseHeader(pingRes, cumsumRes)

	// Setup metrics for both server and client
	serverMetricReader, serverMeterProvider := setupMetrics()
	clientMetricReader, clientMeterProvider := setupMetrics()

	serverInterceptor, err := NewServerInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(handlerTraceProvider),
		WithMeterProvider(serverMeterProvider),
		requestHeaderOption,
		responseHeaderOption,
	)
	require.NoError(t, err)
	clientInterceptor, err := NewClientInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(clientTraceProvider),
		WithMeterProvider(clientMeterProvider),
		requestHeaderOption,
		responseHeaderOption,
	)
	require.NoError(t, err)
	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{serverInterceptor},
		[]connect.ClientInterceptor{clientInterceptor},
		&pluggablePingServer{
			ping: func(ctx context.Context, _ *pingv1.PingRequest) (*pingv1.PingResponse, error) {
				info, _ := connect.CallInfoForServerContext(ctx)
				info.ResponseHeader().Set(pingRes, value)
				info.ResponseHeader().Add(pingRes, value) // Add two values to test formatting
				return &pingv1.PingResponse{}, nil
			},
			pingStream: func(ctx context.Context, stream pingv1connect.PingServicePingStreamServerStream) error {
				info, _ := connect.CallInfoForServerContext(ctx)
				info.ResponseHeader().Set(cumsumRes, value)
				_, _ = stream.Receive()
				return stream.Send(&pingv1.PingStreamResponse{})
			},
		})

	// Set request metadata for unary ping request
	pingCtx := withRequestHeader(context.Background(), pingReq, value)
	_, err = client.Ping(pingCtx, &pingv1.PingRequest{Id: 1})
	require.NoError(t, err)
	// Set request metadata for streaming cumsum
	streamCtx := withRequestHeader(context.Background(), cumsumReq, value)
	stream, err := client.PingStream(streamCtx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{}))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseSend())
	_, err = stream.Receive()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, stream.Close())
	require.Len(t, handlerSpanRecorder.Ended(), 2)
	require.Len(t, clientSpanRecorder.Ended(), 2)
	handlerSpans := handlerSpanRecorder.Ended()
	handlerPingSpan := handlerSpans[0]
	handlerCumsumSpan := handlerSpans[1]
	clientSpans := clientSpanRecorder.Ended()
	clientPingSpan := clientSpans[0]
	clientCumsumSpan := clientSpans[1]
	// Request spans from handler
	require.Contains(t, handlerPingSpan.Attributes(), attributePingReq)
	require.Contains(t, handlerCumsumSpan.Attributes(), attributeCumsumReq)
	// Response spans from handler
	require.Contains(t, handlerPingSpan.Attributes(), attributePingRes)
	require.Contains(t, handlerCumsumSpan.Attributes(), attributeCumsumRes)
	// Request spans from client
	require.Contains(t, clientPingSpan.Attributes(), attributePingReq)
	require.Contains(t, clientCumsumSpan.Attributes(), attributeCumsumReq)
	// Response spans from client
	require.Contains(t, clientPingSpan.Attributes(), attributePingRes)
	require.Contains(t, clientCumsumSpan.Attributes(), attributeCumsumRes)

	// Assert server metrics - should NOT contain header metadata
	assertMetrics(t, serverMetricReader, expectedMetrics{
		ServerDuration:   true,
		NoHeaderMetadata: true,
		RequiredAttrs: map[string]attribute.Value{
			rpcSystemName: attribute.StringValue(connectProtocol),
		},
	})

	// Assert client metrics - should NOT contain header metadata
	assertMetrics(t, clientMetricReader, expectedMetrics{
		ClientDuration:   true,
		NoHeaderMetadata: true,
		RequiredAttrs: map[string]attribute.Value{
			rpcSystemName: attribute.StringValue(connectProtocol),
		},
	})
}

func TestInterceptors(t *testing.T) {
	t.Parallel()
	const largeMessageSize = 1000
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	metricReader, meterProvider := setupMetrics()
	serverInterceptor, err := NewServerInterceptor(
		WithTracerProvider(traceProvider),
		WithMeterProvider(meterProvider),
		WithTraceRequestHeader("X-Request-Id"),
	)
	require.NoError(t, err)
	pingClient, host, port := startServer(t, []connect.ServerInterceptor{serverInterceptor}, nil, okayPingServer())
	ctx := withRequestHeader(context.Background(), "X-Request-Id", "request-123")
	if _, err := pingClient.Ping(ctx, requestOfSize(1, 0)); err != nil {
		t.Error(err)
	}
	if _, err := pingClient.Ping(context.Background(), requestOfSize(2, largeMessageSize)); err != nil {
		t.Error(err)
	}
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + pingMethod,
			attrs: []attribute.KeyValue{
				semconv.RPCSystemNameKey.String(connectProtocol),
				semconv.RPCMethodKey.String(pingProcedure),
				semconv.RPCResponseStatusCodeKey.String(statusCodeOK),
				semconv.NetworkPeerAddressKey.String(host),
				semconv.NetworkPeerPortKey.Int(port),
				attribute.StringSlice("rpc.request.metadata.x-request-id", []string{"request-123"}),
			},
		},
		{
			spanName: pingv1connect.PingServiceName + "/" + pingMethod,
			attrs: []attribute.KeyValue{
				semconv.RPCSystemNameKey.String(connectProtocol),
				semconv.RPCMethodKey.String(pingProcedure),
				semconv.RPCResponseStatusCodeKey.String(statusCodeOK),
				semconv.NetworkPeerAddressKey.String(host),
				semconv.NetworkPeerPortKey.Int(port),
			},
		},
	}, spanRecorder.Ended())

	// Assert metrics - should NOT contain header metadata but should have standard RPC attributes
	assertMetrics(t, metricReader, expectedMetrics{
		ServerDuration:   true,
		NoHeaderMetadata: true,
		RequiredAttrs: map[string]attribute.Value{
			rpcSystemName: attribute.StringValue(connectProtocol),
			rpcMethod:     attribute.StringValue(pingProcedure),
		},
	})
}

func TestUnaryHandlerNoTraceParent(t *testing.T) {
	t.Parallel()
	assertNoTraceParent := func(ctx context.Context, req *pingv1.PingRequest) (*pingv1.PingResponse, error) {
		info, _ := connect.CallInfoForServerContext(ctx)
		require.NotNil(t, info)
		val := info.RequestHeader().Get(traceParentKey)
		assert.Empty(t, val)
		return &pingv1.PingResponse{Id: req.GetId()}, nil
	}
	serverInterceptor, err := NewServerInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(trace.NewTracerProvider()),
	)
	require.NoError(t, err)
	client, _, _ := startServer(t, []connect.ServerInterceptor{serverInterceptor}, nil, &pluggablePingServer{ping: assertNoTraceParent})
	resp, err := client.Ping(context.Background(), &pingv1.PingRequest{Id: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.GetId())
}

func TestStreamingHandlerNoTraceParent(t *testing.T) {
	t.Parallel()
	msg := &pingv1.PingStreamResponse{
		Data: []byte("Hello, otel!"),
	}
	assertNoTraceParent := func(ctx context.Context, stream pingv1connect.PingServicePingStreamServerStream) error {
		info, _ := connect.CallInfoForServerContext(ctx)
		require.NotNil(t, info)
		val := info.RequestHeader().Get(traceParentKey)
		assert.Empty(t, val)
		return stream.Send(msg)
	}
	serverInterceptor, err := NewServerInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(trace.NewTracerProvider()),
	)
	require.NoError(t, err)
	client, _, _ := startServer(t, []connect.ServerInterceptor{serverInterceptor}, nil, &pluggablePingServer{pingStream: assertNoTraceParent})
	stream, err := client.PingStream(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.CloseSend())
	resp, err := stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	assert.Equal(t, msg.GetData(), resp.GetData())
}

func TestPropagationBaggage(t *testing.T) {
	t.Parallel()
	propagator := propagation.NewCompositeTextMapPropagator(propagation.Baggage{}, propagation.TraceContext{})
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	assertBaggageHandler := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			info, _ := connect.CallInfoForServerContext(ctx)
			require.NotNil(t, info)
			assert.Equal(t, "foo=bar", info.RequestHeader().Get("Baggage"))
			return next(ctx, spec, stream)
		}
	}
	assertBaggageClient := func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			info, _ := connect.CallInfoForClientContext(ctx)
			require.NotNil(t, info)
			assert.Equal(t, "foo=bar", info.RequestHeader().Get("Baggage"))
			return next(ctx, spec)
		}
	}
	serverInterceptor, err := NewServerInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(traceProvider),
		WithTrustRemote())
	require.NoError(t, err)
	clientInterceptor, err := NewClientInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(traceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{serverInterceptor, assertBaggageHandler},
		[]connect.ClientInterceptor{clientInterceptor, assertBaggageClient},
		okayPingServer())
	bag, _ := baggage.Parse("foo=bar")
	ctx := baggage.ContextWithBaggage(context.Background(), bag)
	_, err = client.Ping(ctx, &pingv1.PingRequest{Id: 1})
	require.NoError(t, err)
}

func TestUnaryPropagation(t *testing.T) {
	t.Parallel()
	var propagator propagation.TraceContext
	handlerSpanRecorder := tracetest.NewSpanRecorder()
	handlerTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(handlerSpanRecorder))
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	ctx, rootSpan := trace.NewTracerProvider().Tracer("test").Start(context.Background(), "test")
	defer rootSpan.End()
	serverInterceptor, err := NewServerInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(handlerTraceProvider),
		WithTrustRemote(),
	)
	require.NoError(t, err)
	clientInterceptor, err := NewClientInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(clientTraceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{serverInterceptor, assertSpanInterceptor{t: t}.Handler()},
		[]connect.ClientInterceptor{clientInterceptor, assertSpanInterceptor{t: t}.Client()},
		okayPingServer())
	_, err = client.Ping(ctx, &pingv1.PingRequest{Id: 1})
	require.NoError(t, err)
	assert.Len(t, handlerSpanRecorder.Ended(), 1)
	assert.Len(t, clientSpanRecorder.Ended(), 1)
	assertSpanParent(t, rootSpan, clientSpanRecorder.Ended()[0], handlerSpanRecorder.Ended()[0])
}

func TestUnaryInterceptorPropagation(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	var span traceapi.Span
	serverInterceptor, err := NewServerInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(traceProvider),
		WithTrustRemote(),
	)
	require.NoError(t, err)
	startSpan := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			ctx, span = trace.NewTracerProvider().Tracer("test").Start(ctx, "test")
			return next(ctx, spec, stream)
		}
	}
	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{startSpan, serverInterceptor},
		nil, okayPingServer())
	resp, err := client.Ping(context.Background(), &pingv1.PingRequest{Id: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.GetId())
	assert.Len(t, spanRecorder.Ended(), 1)
	recordedSpan := spanRecorder.Ended()[0]
	assert.True(t, recordedSpan.Parent().IsValid())
	assert.True(t, recordedSpan.Parent().Equal(span.SpanContext()))
}

func TestUnaryInterceptorNotModifiedError(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	serverInterceptor, err := NewServerInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(traceProvider),
		WithTrustRemote(),
	)
	require.NoError(t, err)
	startSpan := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			ctx, span := trace.NewTracerProvider().Tracer("test").Start(ctx, "test")
			defer span.End()
			return next(ctx, spec, stream)
		}
	}
	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{startSpan, serverInterceptor},
		nil,
		okayPingServer(),
		connecthttp.WithHTTPGet(),
	)
	ctx := withRequestHeader(context.Background(), "If-None-Match", cacheablePingEtag)
	_, err = client.Ping(ctx, &pingv1.PingRequest{Id: 1})
	require.ErrorContains(t, err, "not modified")
	assert.True(t, connecthttp.IsNotModifiedError(err))
	assert.Len(t, spanRecorder.Ended(), 1)
	recordedSpan := spanRecorder.Ended()[0]
	assert.Equal(t, codes.Unset, recordedSpan.Status().Code)
	var codeAttributes []attribute.KeyValue
	for _, attr := range recordedSpan.Attributes() {
		switch {
		case attr.Key == semconv.HTTPResponseStatusCodeKey,
			attr.Key == semconv.ErrorTypeKey,
			strings.HasPrefix(string(attr.Key), "rpc") && strings.HasSuffix(string(attr.Key), "code"):
			codeAttributes = append(codeAttributes, attr)
		}
	}
	// A not-modified response is a successful RPC that carries the HTTP
	// status as an extension attribute; it must not be reported as an error.
	expectedCodeAttributes := []attribute.KeyValue{
		semconv.RPCResponseStatusCodeKey.String(statusCodeOK),
		semconv.HTTPResponseStatusCodeKey.Int(304),
	}
	assert.Equal(t, expectedCodeAttributes, codeAttributes)
}

func TestWithUntrustedRemoteUnary(t *testing.T) {
	t.Parallel()
	var propagator propagation.TraceContext
	handlerSpanRecorder := tracetest.NewSpanRecorder()
	handlerTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(handlerSpanRecorder))
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	ctx, rootSpan := trace.NewTracerProvider().Tracer("test").Start(context.Background(), "test")
	defer rootSpan.End()
	serverInterceptor, err := NewServerInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(handlerTraceProvider),
	)
	require.NoError(t, err)
	clientInterceptor, err := NewClientInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(clientTraceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{serverInterceptor},
		[]connect.ClientInterceptor{clientInterceptor},
		okayPingServer())
	_, err = client.Ping(ctx, &pingv1.PingRequest{Id: 1})
	require.NoError(t, err)
	assert.Len(t, handlerSpanRecorder.Ended(), 1)
	assert.Len(t, clientSpanRecorder.Ended(), 1)
	assertSpanLink(t, rootSpan, clientSpanRecorder.Ended()[0], handlerSpanRecorder.Ended()[0])
}

func TestStreamingHandlerInterceptorPropagation(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	var span traceapi.Span
	serverInterceptor, err := NewServerInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(traceProvider),
	)
	require.NoError(t, err)
	startSpan := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			ctx, span = trace.NewTracerProvider().Tracer("test").Start(ctx, "test")
			return next(ctx, spec, stream)
		}
	}
	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{startSpan, serverInterceptor},
		nil, okayPingServer())
	stream, err := client.PingStream(context.Background())
	require.NoError(t, err)
	// v2's CloseSend is a no-op when no Send has happened, so we send a
	// message to force the request to flush and the handler to run.
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{}))
	require.NoError(t, stream.CloseSend())
	// Drain the response stream so the handler has finished (and ended its
	// span) before the recorders are inspected.
	for {
		if _, err := stream.Receive(); err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
	}
	require.NoError(t, stream.Close())
	assert.Len(t, spanRecorder.Ended(), 1)
	recordedSpan := spanRecorder.Ended()[0]
	assert.True(t, recordedSpan.Parent().IsValid())
	assert.True(t, recordedSpan.Parent().Equal(span.SpanContext()))
}

func TestStreamingPropagation(t *testing.T) {
	t.Parallel()
	var propagator propagation.TraceContext
	handlerSpanRecorder := tracetest.NewSpanRecorder()
	handlerTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(handlerSpanRecorder))
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	ctx, rootSpan := trace.NewTracerProvider().Tracer("test").Start(context.Background(), "test")
	defer rootSpan.End()
	serverInterceptor, err := NewServerInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(handlerTraceProvider),
		WithTrustRemote(),
	)
	require.NoError(t, err)
	clientInterceptor, err := NewClientInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(clientTraceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{serverInterceptor},
		[]connect.ClientInterceptor{clientInterceptor},
		okayPingServer())
	stream, err := client.PingStream(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{}))
	require.NoError(t, stream.CloseSend())
	// Drain the response stream so the handler has finished (and ended its
	// span) before the recorders are inspected.
	for {
		if _, err := stream.Receive(); err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
	}
	require.NoError(t, stream.Close())
	assert.Len(t, handlerSpanRecorder.Ended(), 1)
	assert.Len(t, clientSpanRecorder.Ended(), 1)
	assertSpanParent(t, rootSpan, clientSpanRecorder.Ended()[0], handlerSpanRecorder.Ended()[0])
}

func TestWithUntrustedRemoteStreaming(t *testing.T) {
	t.Parallel()
	var propagator propagation.TraceContext
	handlerSpanRecorder := tracetest.NewSpanRecorder()
	handlerTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(handlerSpanRecorder))
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	ctx, rootSpan := trace.NewTracerProvider().Tracer("test").Start(context.Background(), "test")
	defer rootSpan.End()
	serverInterceptor, err := NewServerInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(handlerTraceProvider),
	)
	require.NoError(t, err)
	clientInterceptor, err := NewClientInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(clientTraceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{serverInterceptor},
		[]connect.ClientInterceptor{clientInterceptor},
		okayPingServer())
	stream, err := client.PingStream(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{}))
	require.NoError(t, stream.CloseSend())
	// Drain the response stream so the handler has finished (and ended its
	// span) before the recorders are inspected.
	for {
		if _, err := stream.Receive(); err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
	}
	require.NoError(t, stream.Close())
	assert.Len(t, handlerSpanRecorder.Ended(), 1)
	assert.Len(t, clientSpanRecorder.Ended(), 1)
	assertSpanLink(t, rootSpan, clientSpanRecorder.Ended()[0], handlerSpanRecorder.Ended()[0])
}

func TestStreamingClientPropagation(t *testing.T) {
	t.Parallel()
	msg := &pingv1.PingStreamResponse{
		Data: []byte("Hello, otel!"),
	}
	assertTraceParent := func(ctx context.Context, stream pingv1connect.PingServicePingStreamServerStream) error {
		info, _ := connect.CallInfoForServerContext(ctx)
		require.NotNil(t, info)
		val := info.RequestHeader().Get(traceParentKey)
		assert.NotEmpty(t, val)
		require.NoError(t, stream.Send(msg))
		return nil
	}
	clientInterceptor, err := NewClientInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(trace.NewTracerProvider()),
	)
	require.NoError(t, err)
	client, _, _ := startServer(t, nil,
		[]connect.ClientInterceptor{clientInterceptor, assertSpanInterceptor{t: t}.Client()},
		&pluggablePingServer{pingStream: assertTraceParent})
	stream, err := client.PingStream(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{}))
	require.NoError(t, stream.CloseSend())
	resp, err := stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	assert.Equal(t, msg.GetData(), resp.GetData())
}

func TestStreamingClientContextCancellation(t *testing.T) {
	t.Parallel()
	msg := &pingv1.PingStreamResponse{
		Data: []byte("Hello, otel!"),
	}
	server := &pluggablePingServer{
		pingStream: func(_ context.Context, stream pingv1connect.PingServicePingStreamServerStream) error {
			require.NoError(t, stream.Send(msg))
			return errors.New("stream closed") // Simulate error in stream.
		},
	}
	clientInterceptor, err := NewClientInterceptor()
	require.NoError(t, err)
	client, _, _ := startServer(t, nil, []connect.ClientInterceptor{clientInterceptor}, server)
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := client.PingStream(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{}))
	require.NoError(t, stream.CloseSend())
	resp, err := stream.Receive()
	require.NoError(t, err)
	assert.Equal(t, msg.GetData(), resp.GetData())
	// Cancel context in parallel with response receive. Either the context will
	// fail the stream or the error is received. This test is to ensure that the
	// context cancellation does not race with the stream response.
	go cancel()
	runtime.Gosched()
	_, err = stream.Receive()
	require.Error(t, err)
	// Close after cancellation may surface the context error; just ensure
	// it doesn't deadlock or panic.
	_ = stream.Close()
}

func TestStreamingHandlerTracing(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	serverInterceptor, err := NewServerInterceptor(WithTracerProvider(traceProvider))
	require.NoError(t, err)
	pingClient, host, port := startServer(t, []connect.ServerInterceptor{serverInterceptor, assertSpanInterceptor{t: t}.Handler()}, nil, okayPingServer())
	stream, err := pingClient.PingStream(context.Background())
	require.NoError(t, err)

	msg := &pingv1.PingStreamRequest{Data: []byte("Hello, otel!")}
	require.NoError(t, stream.Send(msg))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseSend())
	_, err = stream.Receive()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, stream.Close())
	require.Len(t, spanRecorder.Ended(), 1)
	require.Equal(t, codes.Unset, spanRecorder.Ended()[0].Status().Code)
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + pingStreamMethod,
			attrs: []attribute.KeyValue{
				semconv.RPCSystemNameKey.String(connectProtocol),
				semconv.RPCMethodKey.String(pingStreamProcedure),
				semconv.RPCResponseStatusCodeKey.String(statusCodeOK),
				semconv.NetworkPeerAddressKey.String(host),
				semconv.NetworkPeerPortKey.Int(port),
			},
		},
	}, spanRecorder.Ended())
}

func TestStreamingClientTracing(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	clientInterceptor, err := NewClientInterceptor(WithTracerProvider(traceProvider))
	require.NoError(t, err)
	pingClient, host, port := startServer(t, nil, []connect.ClientInterceptor{clientInterceptor}, okayPingServer())
	stream, err := pingClient.PingStream(context.Background())
	require.NoError(t, err)

	msg := &pingv1.PingStreamRequest{Data: []byte("Hello, otel!")}
	require.NoError(t, stream.Send(msg))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseSend())
	require.NoError(t, stream.Close())
	require.Len(t, spanRecorder.Ended(), 1)
	require.Equal(t, codes.Unset, spanRecorder.Ended()[0].Status().Code)
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + pingStreamMethod,
			attrs: []attribute.KeyValue{
				semconv.RPCSystemNameKey.String(connectProtocol),
				semconv.RPCMethodKey.String(pingStreamProcedure),
				semconv.ServerAddressKey.String(host),
				semconv.ServerPortKey.Int(port),
				semconv.RPCResponseStatusCodeKey.String(statusCodeOK),
			},
		},
	}, spanRecorder.Ended())
}

func TestWithAttributeFilter(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	clientInterceptor, err := NewClientInterceptor(
		WithTracerProvider(traceProvider),
		WithAttributeFilter(func(_ connect.Spec, value attribute.KeyValue) bool {
			return value.Key != semconv.ServerPortKey
		}),
	)
	require.NoError(t, err)
	pingClient, host, _ := startServer(t, nil, []connect.ClientInterceptor{clientInterceptor}, okayPingServer())
	stream, err := pingClient.PingStream(context.Background())
	require.NoError(t, err)

	msg := &pingv1.PingStreamRequest{Data: []byte("Hello, otel!")}
	require.NoError(t, stream.Send(msg))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseSend())
	require.NoError(t, stream.Close())
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + pingStreamMethod,
			attrs: []attribute.KeyValue{
				semconv.RPCSystemNameKey.String(connectProtocol),
				semconv.RPCMethodKey.String(pingStreamProcedure),
				semconv.ServerAddressKey.String(host),
				semconv.RPCResponseStatusCodeKey.String(statusCodeOK),
			},
		},
	}, spanRecorder.Ended())
}

func TestWithoutServerPeerAttributes(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	serverInterceptor, err := NewServerInterceptor(
		WithTracerProvider(traceProvider),
		WithoutServerPeerAttributes(),
	)
	require.NoError(t, err)
	pingClient, _, _ := startServer(t, []connect.ServerInterceptor{serverInterceptor}, nil, okayPingServer())
	stream, err := pingClient.PingStream(context.Background())
	require.NoError(t, err)
	msg := &pingv1.PingStreamRequest{Data: []byte("Hello, otel!")}
	require.NoError(t, stream.Send(msg))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseSend())
	_, err = stream.Receive()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, stream.Close())
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + pingStreamMethod,
			attrs: []attribute.KeyValue{
				semconv.RPCSystemNameKey.String(connectProtocol),
				semconv.RPCMethodKey.String(pingStreamProcedure),
				semconv.RPCResponseStatusCodeKey.String(statusCodeOK),
			},
		},
	}, spanRecorder.Ended())
}

func TestStreamingSpanStatus(t *testing.T) {
	t.Parallel()
	var propagator propagation.TraceContext
	handlerSpanRecorder := tracetest.NewSpanRecorder()
	handlerTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(handlerSpanRecorder))
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	serverInterceptor, err := NewServerInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(handlerTraceProvider),
	)
	require.NoError(t, err)
	clientInterceptor, err := NewClientInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(clientTraceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{serverInterceptor},
		[]connect.ClientInterceptor{clientInterceptor},
		failPingServer())
	stream, err := client.PingStream(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{
		Data: []byte("Hello, otel!"),
	}))
	_, err = stream.Receive()
	require.Error(t, err)
	require.NoError(t, stream.CloseSend())
	require.NoError(t, stream.Close())
	assert.Len(t, handlerSpanRecorder.Ended(), 1)
	assert.Len(t, clientSpanRecorder.Ended(), 1)
	assert.Equal(t, codes.Error, handlerSpanRecorder.Ended()[0].Status().Code)
	assert.Equal(t, codes.Error, clientSpanRecorder.Ended()[0].Status().Code)
}

func TestServerSpanStatus(t *testing.T) {
	t.Parallel()
	var propagator propagation.TraceContext
	for _, testcase := range serverSpanStatusTestCases() {
		spanRecorder := tracetest.NewSpanRecorder()
		traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
		clientSpanRecorder := tracetest.NewSpanRecorder()
		clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
		serverInterceptor, err := NewServerInterceptor(
			WithTracerProvider(traceProvider),
		)
		require.NoError(t, err)
		clientInterceptor, err := NewClientInterceptor(
			WithPropagator(propagator),
			WithTracerProvider(clientTraceProvider),
		)
		require.NoError(t, err)
		pingClient, _, _ := startServer(t,
			[]connect.ServerInterceptor{serverInterceptor},
			[]connect.ClientInterceptor{clientInterceptor},
			&pluggablePingServer{
				ping: func(_ context.Context, _ *pingv1.PingRequest) (*pingv1.PingResponse, error) {
					return nil, connect.NewError(testcase.connectCode, testcase.connectCode.String())
				},
			})
		_, err = pingClient.Ping(context.Background(), requestOfSize(1, 0))
		require.Error(t, err)
		require.Len(t, spanRecorder.Ended(), 1)
		require.Equal(t, codes.Error, clientSpanRecorder.Ended()[0].Status().Code)
		require.Equal(t, testcase.wantServerSpanCode, spanRecorder.Ended()[0].Status().Code)
		require.Equal(t, testcase.wantServerSpanDescription, spanRecorder.Ended()[0].Status().Description)
	}
}

func TestStreamingServerSpanStatus(t *testing.T) {
	t.Parallel()
	var propagator propagation.TraceContext
	for _, testcase := range serverSpanStatusTestCases() {
		handlerSpanRecorder := tracetest.NewSpanRecorder()
		handlerTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(handlerSpanRecorder))
		clientSpanRecorder := tracetest.NewSpanRecorder()
		clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
		serverInterceptor, err := NewServerInterceptor(
			WithTracerProvider(handlerTraceProvider),
		)
		require.NoError(t, err)
		clientInterceptor, err := NewClientInterceptor(
			WithPropagator(propagator),
			WithTracerProvider(clientTraceProvider),
		)
		require.NoError(t, err)
		client, _, _ := startServer(t,
			[]connect.ServerInterceptor{serverInterceptor},
			[]connect.ClientInterceptor{clientInterceptor},
			&pluggablePingServer{
				pingStream: func(_ context.Context, stream pingv1connect.PingServicePingStreamServerStream) error {
					_, _ = stream.Receive()
					return connect.NewError(testcase.connectCode, testcase.connectCode.String())
				},
			})
		stream, err := client.PingStream(t.Context())
		require.NoError(t, err)
		require.NoError(t, stream.Send(&pingv1.PingStreamRequest{
			Data: []byte("Hello, otel!"),
		}))
		_, err = stream.Receive()
		require.Error(t, err)
		require.NoError(t, stream.CloseSend())
		require.NoError(t, stream.Close())
		assert.Len(t, handlerSpanRecorder.Ended(), 1)
		assert.Len(t, clientSpanRecorder.Ended(), 1)
		assert.Equal(t, testcase.wantServerSpanCode, handlerSpanRecorder.Ended()[0].Status().Code)
		assert.Equal(t, testcase.wantServerSpanDescription, handlerSpanRecorder.Ended()[0].Status().Description)
		assert.Equal(t, codes.Error, clientSpanRecorder.Ended()[0].Status().Code)
	}
}

func TestWithRPCSystem(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		system         RPCSystem
		expectProtocol string
	}{
		{
			system:         nil,
			expectProtocol: "", // depends on request
		},
		{
			system:         ConnectRPCSystem,
			expectProtocol: connectProtocol,
		},
		{
			system:         GRPCSystem,
			expectProtocol: grpcProtocol,
		},
	}
	clients := []struct {
		protocol     string
		expectSystem RPCSystem
		opt          connecthttp.Option
	}{
		{
			protocol:     connect.ProtocolNameConnect,
			expectSystem: ConnectRPCSystem,
		},
		{
			protocol:     connect.ProtocolNameGRPC,
			expectSystem: GRPCSystem,
			opt:          connecthttp.WithGRPC(),
		},
		{
			protocol:     connect.ProtocolNameGRPCWeb,
			expectSystem: GRPCSystem,
			opt:          connecthttp.WithGRPCWeb(),
		},
	}
	for _, testCase := range testCases {
		name := "based-on-wire-request"
		if testCase.system != nil {
			name = testCase.system.protocol()
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, clientCase := range clients {
				t.Run("client="+clientCase.protocol, func(t *testing.T) {
					t.Parallel()
					var opts []connecthttp.Option
					if clientCase.opt != nil {
						opts = []connecthttp.Option{clientCase.opt}
					}
					metricReader := metricsdk.NewManualReader()
					meterProvider := metricsdk.NewMeterProvider(
						metricsdk.WithReader(
							metricReader,
						),
					)
					interceptor, err := NewServerInterceptor(
						WithMeterProvider(meterProvider),
						WithRPCSystem(testCase.system),
					)
					require.NoError(t, err)
					handlerInterceptors := []connect.ServerInterceptor{interceptor}
					// Use separate servers for unary and streaming calls.
					// A failed gRPC unary call can leave the HTTP/2
					// connection in a state where new streams fail.
					unaryClient, _, _ := startServer(t, handlerInterceptors, nil, failPingServer(), opts...)
					_, err = unaryClient.Ping(t.Context(), &pingv1.PingRequest{})
					require.Equal(t, connect.CodeDataLoss, connect.CodeOf(err))
					streamClient, _, _ := startServer(t, handlerInterceptors, nil, failPingServer(), opts...)
					bidiStream, err := streamClient.PingStream(t.Context())
					require.NoError(t, err)
					defer func() {
						_ = bidiStream.Close()
					}()
					// Send may fail if the server terminates the stream first;
					// the real error is surfaced by Receive.
					_ = bidiStream.Send(&pingv1.PingStreamRequest{})
					require.NoError(t, bidiStream.CloseSend())
					_, err = bidiStream.Receive()
					require.Equal(t, connect.CodeDataLoss, connect.CodeOf(err))

					expectedMetricsConventions := testCase.system
					if expectedMetricsConventions == nil {
						expectedMetricsConventions = clientCase.expectSystem
					}
					metrics := &metricdata.ResourceMetrics{}
					require.NoError(t, metricReader.Collect(context.Background(), metrics))

					// Only rpc.system.name varies by RPC system; the status
					// code is the uppercase connect code for every system.
					require.Len(t, metrics.ScopeMetrics, 1)
					require.NotEmpty(t, metrics.ScopeMetrics[0].Metrics)
					for _, metric := range metrics.ScopeMetrics[0].Metrics {
						histo, ok := metric.Data.(metricdata.Histogram[float64])
						require.True(t, ok)
						require.NotEmpty(t, histo.DataPoints)
						for _, dataPoint := range histo.DataPoints {
							systemName, found := dataPoint.Attributes.Value(rpcSystemName)
							require.True(t, found)
							require.Equal(t, expectedMetricsConventions.protocol(), systemName.AsString())
							statusCode, found := dataPoint.Attributes.Value(rpcStatusCode)
							require.True(t, found)
							require.Equal(t, dataLossString, statusCode.AsString())
							errType, found := dataPoint.Attributes.Value(errorType)
							require.True(t, found)
							require.Equal(t, dataLossString, errType.AsString())
						}
					}
				})
			}
		})
	}
}

type wantSpans struct {
	spanName string
	attrs    []attribute.KeyValue
}

func assertSpans(t *testing.T, want []wantSpans, got []trace.ReadOnlySpan) {
	t.Helper()
	require.Len(t, got, len(want), "unexpected spans length")
	for i, span := range got {
		wantSpan := want[i] //nolint: gosec // index bounds asserted above
		wantAttributes := wantSpan.attrs
		assert.False(t, span.StartTime().IsZero(), "span start time is nil")
		assert.Equal(t, wantSpan.spanName, span.Name(), "unexpected span name")
		assert.Empty(t, span.Events(), "unexpected span events")
		// Attribute order is not significant. The server's view of the
		// peer port is the client's ephemeral port, so only its presence is
		// checked.
		diff := cmp.Diff(wantAttributes, span.Attributes(),
			cmpopts.IgnoreUnexported(attribute.Value{}),
			cmpopts.SortSlices(func(x, y attribute.KeyValue) bool {
				return x.Key < y.Key
			}),
			cmp.Comparer(func(x, y attribute.KeyValue) bool {
				if x.Key == semconv.NetworkPeerPortKey && y.Key == semconv.NetworkPeerPortKey {
					return true
				}
				return x.Key == y.Key && x.Value == y.Value
			},
			))
		assert.Empty(t, diff)
	}
}

func assertSpanParent(t *testing.T, rootSpan traceapi.Span, clientSpan trace.ReadOnlySpan, handlerSpan trace.ReadOnlySpan) {
	t.Helper()
	assert.True(t, handlerSpan.Parent().IsRemote())
	assert.False(t, clientSpan.SpanContext().IsRemote())
	assert.True(t, clientSpan.SpanContext().IsValid())
	assert.True(t, clientSpan.SpanContext().IsValid())
	assert.True(t, clientSpan.Parent().Equal(rootSpan.SpanContext()))
	assert.Equal(t, clientSpan.SpanContext().TraceID(), handlerSpan.SpanContext().TraceID())
}

func assertSpanLink(t *testing.T, rootSpan traceapi.Span, clientSpan trace.ReadOnlySpan, handlerSpan trace.ReadOnlySpan) {
	t.Helper()
	assert.False(t, handlerSpan.Parent().IsValid())
	assert.False(t, clientSpan.SpanContext().IsRemote())
	assert.True(t, clientSpan.SpanContext().IsValid())
	assert.True(t, clientSpan.Parent().Equal(rootSpan.SpanContext()))
	// The client was the invoker, so the root TraceID and the client TraceID should be the same.
	assert.Equal(t, rootSpan.SpanContext().TraceID(), clientSpan.SpanContext().TraceID())
	assert.NotEqual(t, clientSpan.SpanContext().TraceID(), handlerSpan.SpanContext().TraceID())
	assert.Len(t, handlerSpan.Links(), 1)
	assert.Equal(t, handlerSpan.Links()[0].SpanContext.TraceID(), clientSpan.SpanContext().TraceID())
	assert.Equal(t, handlerSpan.Links()[0].SpanContext.SpanID(), clientSpan.SpanContext().SpanID())
}

func startServer(t *testing.T, serverInterceptors []connect.ServerInterceptor, clientInterceptors []connect.ClientInterceptor, svc pingv1connect.PingServiceHandler, transportOpts ...connecthttp.Option) (pingv1connect.PingServiceClient, string, int) {
	t.Helper()
	mux := http.NewServeMux()
	v2server := connect.NewServer(serverInterceptors...)
	pingv1connect.RegisterPingServiceHandler(v2server, svc)
	connecthttp.Mount(mux, v2server)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	transport := connecthttp.NewTransport(server.Client(), server.URL, transportOpts...)
	client := connect.NewClient(transport, clientInterceptors...)
	host, port, err := net.SplitHostPort(strings.ReplaceAll(server.URL, "https://", ""))
	require.NoError(t, err)
	portint, err := strconv.Atoi(port)
	require.NoError(t, err)
	return pingv1connect.NewPingServiceClient(client), host, portint
}

func requestOfSize(id, dataSize int64) *pingv1.PingRequest {
	body := make([]byte, dataSize)
	for i := range body {
		body[i] = byte(rand.Intn(128)) //nolint: gosec
	}
	return &pingv1.PingRequest{Id: id, Data: body}
}

// withRequestHeader attaches a client-side CallInfo to ctx (if not already
// present) and sets the named header. Tests that need to set request headers
// before calling client.Ping(...) use this in place of v1's req.Header().Set.
func withRequestHeader(ctx context.Context, key, value string) context.Context {
	info, ok := connect.CallInfoForClientContext(ctx)
	if !ok {
		ctx, info = connect.NewClientContext(ctx)
	}
	info.RequestHeader().Set(key, value)
	return ctx
}

type optionFunc func(*config)

func (o optionFunc) apply(c *config) {
	o(c)
}

func cmpOpts() []cmp.Option {
	return []cmp.Option{
		cmp.Comparer(func(setx, sety attribute.Set) bool {
			return setx.Equals(&sety)
		}),
		cmp.Comparer(func(extx, exty metricdata.Extrema[float64]) bool {
			valx, definedx := extx.Value()
			valy, definedy := exty.Value()
			return valx == valy && definedx == definedy
		}),
		cmpopts.EquateEmpty(),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint[float64]{}, "StartTime"),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint[float64]{}, "Time"),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint[float64]{}, "Bounds"),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint[float64]{}, "BucketCounts"),
	}
}

// withSecondTicks replaces the interceptor clock with one that advances a
// second per call, so every recorded duration is exactly one second.
func withSecondTicks() Option {
	var now time.Time
	return optionFunc(func(c *config) {
		c.now = func() time.Time {
			now = now.Add(time.Second)
			return now
		}
	})
}

// expectedDurationMetrics builds the metrics an interceptor using
// withSecondTicks is expected to record for a single RPC: one call duration
// histogram, named and described by the semantic conventions, with a single
// one-second data point carrying attrs.
func expectedDurationMetrics(side string, attrs ...attribute.KeyValue) *metricdata.ResourceMetrics {
	name, description := rpcconv.ClientCallDuration{}.Name(), rpcconv.ClientCallDuration{}.Description()
	if side == serverKey {
		name, description = rpcconv.ServerCallDuration{}.Name(), rpcconv.ServerCallDuration{}.Description()
	}
	return &metricdata.ResourceMetrics{
		Resource: metricResource(),
		ScopeMetrics: []metricdata.ScopeMetrics{
			{
				Scope: instrumentation.Scope{
					Name:    instrumentationName,
					Version: semanticVersion,
				},
				Metrics: []metricdata.Metrics{
					{
						Name:        name,
						Description: description,
						Unit:        "s",
						Data: metricdata.Histogram[float64]{
							DataPoints: []metricdata.HistogramDataPoint[float64]{
								{
									Attributes: attribute.NewSet(attrs...),
									Count:      1,
									Sum:        1,
									Min:        metricdata.NewExtrema(1.0),
									Max:        metricdata.NewExtrema(1.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
				},
			},
		},
	}
}

func setupMetrics() (metricsdk.Reader, *metricsdk.MeterProvider) {
	metricReader := metricsdk.NewManualReader()
	meterProvider := metricsdk.NewMeterProvider(
		metricsdk.WithReader(metricReader),
		metricsdk.WithResource(metricResource()),
	)
	return metricReader, meterProvider
}

type expectedMetrics struct {
	ServerDuration   bool
	ClientDuration   bool
	NoHeaderMetadata bool
	RequiredAttrs    map[string]attribute.Value
}

// assertMetrics verifies that metrics are collected with expected attributes.
func assertMetrics(t *testing.T, metricReader metricsdk.Reader, expected expectedMetrics) {
	t.Helper()
	metrics := &metricdata.ResourceMetrics{}
	require.NoError(t, metricReader.Collect(context.Background(), metrics))

	foundMetrics := make(map[string]bool)

	for _, scopeMetric := range metrics.ScopeMetrics {
		for _, metric := range scopeMetric.Metrics {
			switch {
			case expected.ServerDuration && metric.Name == rpcServerDuration:
				foundMetrics[rpcServerDuration] = true
				assertMetricAttributes(t, metric, expected.NoHeaderMetadata, expected.RequiredAttrs)
			case expected.ClientDuration && metric.Name == rpcClientDuration:
				foundMetrics[rpcClientDuration] = true
				assertMetricAttributes(t, metric, expected.NoHeaderMetadata, expected.RequiredAttrs)
			}
		}
	}

	if expected.ServerDuration {
		assert.True(t, foundMetrics[rpcServerDuration], "Should find server duration metrics")
	}
	if expected.ClientDuration {
		assert.True(t, foundMetrics[rpcClientDuration], "Should find client duration metrics")
	}
}

func assertMetricAttributes(t *testing.T, metric metricdata.Metrics, noHeaderMetadata bool, requiredAttrs map[string]attribute.Value) {
	t.Helper()
	if histogram, ok := metric.Data.(metricdata.Histogram[float64]); ok {
		for _, dataPoint := range histogram.DataPoints {
			attrs := dataPoint.Attributes.ToSlice()

			if noHeaderMetadata {
				// Verify that header metadata is NOT present in metrics
				for _, attr := range attrs {
					assert.NotContains(t, string(attr.Key), "rpc.request.metadata",
						"Metric attributes should not contain request header metadata")
					assert.NotContains(t, string(attr.Key), "rpc.response.metadata",
						"Metric attributes should not contain response header metadata")
				}
			}

			// Verify required attributes are present
			if len(requiredAttrs) > 0 {
				attrMap := make(map[string]attribute.Value)
				for _, attr := range attrs {
					attrMap[string(attr.Key)] = attr.Value
				}
				for key, expectedValue := range requiredAttrs {
					actualValue, exists := attrMap[key]
					assert.True(t, exists, "Required attribute %s should be present", key)
					if exists {
						assert.Equal(t, expectedValue, actualValue, "Attribute %s should have expected value", key)
					}
				}
			}
		}
	}
}

func metricResource() *resource.Resource {
	return resource.NewWithAttributes("https://opentelemetry.io/schemas/1.12.0",
		attribute.String("service.name", "test"),
		attribute.String("telemetry.sdk.language", "go"),
		attribute.String("telemetry.sdk.name", "opentelemetry"),
		attribute.String("telemetry.sdk.version", otel.Version()),
	)
}

type serverSpanStatusTestCase struct {
	connectCode               connect.Code
	wantServerSpanCode        codes.Code
	wantServerSpanDescription string
}

func serverSpanStatusTestCases() []serverSpanStatusTestCase {
	return []serverSpanStatusTestCase{
		{connectCode: connect.CodeCanceled, wantServerSpanCode: codes.Unset, wantServerSpanDescription: ""},
		{connectCode: connect.CodeUnknown, wantServerSpanCode: codes.Error, wantServerSpanDescription: connect.CodeUnknown.String()},
		{connectCode: connect.CodeInvalidArgument, wantServerSpanCode: codes.Unset, wantServerSpanDescription: ""},
		{connectCode: connect.CodeDeadlineExceeded, wantServerSpanCode: codes.Error, wantServerSpanDescription: connect.CodeDeadlineExceeded.String()},
		{connectCode: connect.CodeNotFound, wantServerSpanCode: codes.Unset, wantServerSpanDescription: ""},
		{connectCode: connect.CodeAlreadyExists, wantServerSpanCode: codes.Unset, wantServerSpanDescription: ""},
		{connectCode: connect.CodePermissionDenied, wantServerSpanCode: codes.Unset, wantServerSpanDescription: ""},
		{connectCode: connect.CodeResourceExhausted, wantServerSpanCode: codes.Unset, wantServerSpanDescription: ""},
		{connectCode: connect.CodeFailedPrecondition, wantServerSpanCode: codes.Unset, wantServerSpanDescription: ""},
		{connectCode: connect.CodeAborted, wantServerSpanCode: codes.Unset, wantServerSpanDescription: ""},
		{connectCode: connect.CodeOutOfRange, wantServerSpanCode: codes.Unset, wantServerSpanDescription: ""},
		{connectCode: connect.CodeUnimplemented, wantServerSpanCode: codes.Error, wantServerSpanDescription: connect.CodeUnimplemented.String()},
		{connectCode: connect.CodeInternal, wantServerSpanCode: codes.Error, wantServerSpanDescription: connect.CodeInternal.String()},
		{connectCode: connect.CodeUnavailable, wantServerSpanCode: codes.Error, wantServerSpanDescription: connect.CodeUnavailable.String()},
		{connectCode: connect.CodeDataLoss, wantServerSpanCode: codes.Error, wantServerSpanDescription: connect.CodeDataLoss.String()},
		{connectCode: connect.CodeUnauthenticated, wantServerSpanCode: codes.Unset, wantServerSpanDescription: ""},
	}
}

// assertSpanInterceptor returns a paired (client, handler) interceptor that
// fails the test if the span context is not valid in the call ctx.
type assertSpanInterceptor struct{ t testing.TB }

func (i assertSpanInterceptor) Client() connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			i.assertSpanContext(ctx)
			return next(ctx, spec)
		}
	}
}

func (i assertSpanInterceptor) Handler() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			i.assertSpanContext(ctx)
			return next(ctx, spec, stream)
		}
	}
}

func (i assertSpanInterceptor) assertSpanContext(ctx context.Context) {
	if !traceapi.SpanContextFromContext(ctx).IsValid() {
		i.t.Error("invalid span context")
	}
}

func TestPropagateResponseHeader(t *testing.T) {
	t.Parallel()

	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))

	serverInterceptor, err := NewServerInterceptor(
		WithPropagateResponseHeader(),
		WithTracerProvider(traceProvider),
		WithPropagator(propagation.TraceContext{}),
	)
	require.NoError(t, err)

	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{serverInterceptor}, nil,
		&pluggablePingServer{
			ping: func(_ context.Context, _ *pingv1.PingRequest) (*pingv1.PingResponse, error) {
				return &pingv1.PingResponse{}, nil
			},
		})

	ctx, info := connect.NewClientContext(context.Background())
	_, err = client.Ping(ctx, &pingv1.PingRequest{Id: 1})
	require.NoError(t, err)

	// Check that the traceparent header is present in the response
	traceparent := info.ResponseHeader().Get("Traceparent")
	assert.NotEmpty(t, traceparent, "traceparent header should be present in response")

	// Validate traceparent
	assertUsableTraceparent(t, info.ResponseHeader())
}

func TestPropagateResponseHeaderStreaming(t *testing.T) {
	t.Parallel()

	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))

	serverInterceptor, err := NewServerInterceptor(
		WithPropagateResponseHeader(),
		WithTracerProvider(traceProvider),
		WithPropagator(propagation.TraceContext{}),
	)
	require.NoError(t, err)

	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{serverInterceptor}, nil,
		&pluggablePingServer{
			pingStream: func(_ context.Context, stream pingv1connect.PingServicePingStreamServerStream) error {
				_, _ = stream.Receive()
				return stream.Send(&pingv1.PingStreamResponse{})
			},
		})

	ctx, info := connect.NewClientContext(context.Background())
	stream, err := client.PingStream(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{}))
	require.NoError(t, stream.CloseSend())
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.Close())

	// Check that the traceparent header is present in the response headers
	traceparent := info.ResponseHeader().Get("Traceparent")
	assert.NotEmpty(t, traceparent, "traceparent header should be present in streaming response")

	// Validate traceparent
	assertUsableTraceparent(t, info.ResponseHeader())
}

// assertUsableTraceparent validates that a traceparent header can be used fromthe response.
func assertUsableTraceparent(t *testing.T, metadata *connect.Header) {
	t.Helper()

	// Use the same propagator that was configured in the test
	tc := propagation.TraceContext{}
	ctx := tc.Extract(context.Background(), metadataCarrier{m: metadata})
	// Ensure the span context is valid
	spanContext := traceapi.SpanContextFromContext(ctx)
	assert.True(t, spanContext.IsValid(), "span context should be valid after extracting traceparent")
	// Ensure the trace ID and span ID are not empty
	assert.NotEmpty(t, spanContext.SpanID(), "span ID should not be empty")
	assert.NotEmpty(t, spanContext.TraceID(), "trace ID should not be empty")
}

// labelerInterceptor is a test interceptor that retrieves the Labeler from
// context and adds custom attributes. Used to test that labeler attributes
// appear in metrics but not in spans.
type labelerInterceptor struct {
	attrs []attribute.KeyValue
}

func (l labelerInterceptor) Client() connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			labeler, _ := LabelerFromContext(ctx)
			labeler.Add(l.attrs...)
			return next(ctx, spec)
		}
	}
}

func (l labelerInterceptor) Handler() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			labeler, _ := LabelerFromContext(ctx)
			labeler.Add(l.attrs...)
			return next(ctx, spec, stream)
		}
	}
}

func TestLabelerFromContext(t *testing.T) {
	t.Parallel()
	// LabelerFromContext on empty context returns a new Labeler and false.
	labeler, ok := LabelerFromContext(context.Background())
	assert.False(t, ok)
	require.NotNil(t, labeler)
	// Add and Get should not panic even though the labeler is not in a context.
	labeler.Add(attribute.String("key", "value"))
	got := labeler.Get()
	assert.Len(t, got, 1)
	assert.Equal(t, attribute.String("key", "value"), got[0])
}

func TestLabelerUnary(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	customAttrs := []attribute.KeyValue{
		attribute.String("custom.label", "test-value"),
	}
	interceptor, err := NewServerInterceptor(
		WithMeterProvider(meterProvider),
		WithTracerProvider(traceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{interceptor, labelerInterceptor{attrs: customAttrs}.Handler()},
		nil,
		okayPingServer(),
	)
	_, err = client.Ping(context.Background(), requestOfSize(1, 12))
	require.NoError(t, err)
	assertMetrics(t, metricReader, expectedMetrics{
		ServerDuration: true,
		RequiredAttrs: map[string]attribute.Value{
			"custom.label": attribute.StringValue("test-value"),
		},
	})
	require.Len(t, spanRecorder.Ended(), 1)
	for _, attr := range spanRecorder.Ended()[0].Attributes() {
		assert.NotEqual(t, attribute.Key("custom.label"), attr.Key,
			"span should not contain labeler attributes")
	}
}

func TestLabelerStreaming(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	customAttrs := []attribute.KeyValue{
		attribute.String("custom.label", "stream-value"),
	}
	interceptor, err := NewServerInterceptor(
		WithMeterProvider(meterProvider),
		WithTracerProvider(traceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(t,
		[]connect.ServerInterceptor{interceptor, labelerInterceptor{attrs: customAttrs}.Handler()},
		nil,
		okayPingServer(),
	)
	stream, err := client.PingStream(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{
		Data: []byte("Hello, otel!"),
	}))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseSend())
	_, err = stream.Receive()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, stream.Close())
	assertMetrics(t, metricReader, expectedMetrics{
		ServerDuration: true,
		RequiredAttrs: map[string]attribute.Value{
			"custom.label": attribute.StringValue("stream-value"),
		},
	})
	require.Len(t, spanRecorder.Ended(), 1)
	for _, attr := range spanRecorder.Ended()[0].Attributes() {
		assert.NotEqual(t, attribute.Key("custom.label"), attr.Key,
			"span should not contain labeler attributes")
	}
}

func TestLabelerUnaryClient(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	customAttrs := []attribute.KeyValue{
		attribute.String("custom.label", "client-value"),
	}
	interceptor, err := NewClientInterceptor(
		WithMeterProvider(meterProvider),
		WithTracerProvider(traceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(t, nil,
		[]connect.ClientInterceptor{interceptor, labelerInterceptor{attrs: customAttrs}.Client()},
		okayPingServer(),
	)
	_, err = client.Ping(context.Background(), requestOfSize(1, 12))
	require.NoError(t, err)
	assertMetrics(t, metricReader, expectedMetrics{
		ClientDuration: true,
		RequiredAttrs: map[string]attribute.Value{
			"custom.label": attribute.StringValue("client-value"),
		},
	})
	require.Len(t, spanRecorder.Ended(), 1)
	for _, attr := range spanRecorder.Ended()[0].Attributes() {
		assert.NotEqual(t, attribute.Key("custom.label"), attr.Key,
			"span should not contain labeler attributes")
	}
}

func TestLabelerStreamingClient(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	customAttrs := []attribute.KeyValue{
		attribute.String("custom.label", "client-stream-value"),
	}
	interceptor, err := NewClientInterceptor(
		WithMeterProvider(meterProvider),
		WithTracerProvider(traceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(t, nil,
		[]connect.ClientInterceptor{interceptor, labelerInterceptor{attrs: customAttrs}.Client()},
		okayPingServer(),
	)
	stream, err := client.PingStream(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{
		Data: []byte("Hello, otel!"),
	}))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseSend())
	require.NoError(t, stream.Close())
	assertMetrics(t, metricReader, expectedMetrics{
		ClientDuration: true,
		RequiredAttrs: map[string]attribute.Value{
			"custom.label": attribute.StringValue("client-stream-value"),
		},
	})
	require.Len(t, spanRecorder.Ended(), 1)
	for _, attr := range spanRecorder.Ended()[0].Attributes() {
		assert.NotEqual(t, attribute.Key("custom.label"), attr.Key,
			"span should not contain labeler attributes")
	}
}
