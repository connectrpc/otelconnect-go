// Copyright 2022-2023 The Connect Authors
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
	"strconv"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	pingv1 "connectrpc.com/otelconnect/internal/gen/observability/ping/v1"
	"connectrpc.com/otelconnect/internal/gen/observability/ping/v1/pingv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	traceapi "go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

const (
	bufConnect               = "connect_rpc"
	PingMethod               = "Ping"
	FailMethod               = "Fail"
	PingStreamMethod         = "PingStream"
	UnimplementedString      = "unimplemented"
	TraceParentKey           = "traceparent"
	rpcClientDuration        = "rpc.client.duration"
	rpcClientRequestSize     = "rpc.client.request.size"
	rpcClientResponseSize    = "rpc.client.response.size"
	rpcClientRequestsPerRPC  = "rpc.client.requests_per_rpc"
	rpcClientResponsesPerRPC = "rpc.client.responses_per_rpc"
	rpcServerRequestSize     = "rpc.server.request.size"
	rpcServerDuration        = "rpc.server.duration"
	rpcServerResponseSize    = "rpc.server.response.size"
	rpcServerRequestsPerRPC  = "rpc.server.requests_per_rpc"
	rpcServerResponsesPerRPC = "rpc.server.responses_per_rpc"
	rpcBufConnectStatusCode  = "rpc.connect_rpc.error_code"
)

func TestStreamingMetrics(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	var now time.Time
	interceptor, err := NewInterceptor(
		WithMeterProvider(meterProvider), optionFunc(func(c *config) {
			c.now = func() time.Time {
				now = now.Add(time.Second)
				return now
			}
		}),
	)
	require.NoError(t, err)
	connectClient, host, port := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(interceptor),
		}, []connect.ClientOption{}, okayPingServer())
	stream := connectClient.PingStream(context.Background())
	msg := &pingv1.PingStreamRequest{
		Data: []byte("Hello, otel!"),
	}
	require.NoError(t, stream.Send(msg))
	size := int64(proto.Size(msg))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
	metrics := &metricdata.ResourceMetrics{}
	assert.NoError(t, metricReader.Collect(context.Background(), metrics))
	diff := cmp.Diff(&metricdata.ResourceMetrics{
		Resource: metricResource(),
		ScopeMetrics: []metricdata.ScopeMetrics{
			{
				Scope: instrumentation.Scope{
					Name:    instrumentationName,
					Version: semanticVersion,
				},
				Metrics: []metricdata.Metrics{
					{
						Name: rpcServerDuration,
						Unit: unitMilliseconds,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   1000.0,
									Min:   metricdata.NewExtrema[int64](1000),
									Max:   metricdata.NewExtrema[int64](1000),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcServerRequestSize,
						Unit: unitBytes,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   size,
									Min:   metricdata.NewExtrema[int64](size),
									Max:   metricdata.NewExtrema[int64](size),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcServerResponseSize,
						Unit: unitBytes,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   size,
									Min:   metricdata.NewExtrema[int64](size),
									Max:   metricdata.NewExtrema[int64](size),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcServerRequestsPerRPC,
						Unit: unitDimensionless,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   1,
									Min:   metricdata.NewExtrema[int64](1),
									Max:   metricdata.NewExtrema[int64](1),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcServerResponsesPerRPC,
						Unit: unitDimensionless,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   1,
									Min:   metricdata.NewExtrema[int64](1),
									Max:   metricdata.NewExtrema[int64](1),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
				},
			},
		},
	},
		metrics,
		cmpOpts()...,
	)
	if diff != "" {
		t.Error(diff)
	}
}

func TestStreamingMetricsClient(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	var now time.Time
	interceptor, err := NewInterceptor(
		WithMeterProvider(meterProvider), optionFunc(func(c *config) {
			c.now = func() time.Time {
				now = now.Add(time.Second)
				return now
			}
		}),
	)
	require.NoError(t, err)
	connectClient, host, port := startServer(
		[]connect.HandlerOption{},
		[]connect.ClientOption{
			connect.WithInterceptors(interceptor),
		}, okayPingServer())
	stream := connectClient.PingStream(context.Background())
	msg := &pingv1.PingStreamRequest{
		Data: []byte("Hello, otel!"),
	}
	size := int64(proto.Size(msg))
	require.NoError(t, stream.Send(msg))
	require.NoError(t, stream.CloseRequest())
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseResponse())
	metrics := &metricdata.ResourceMetrics{}
	require.NoError(t, metricReader.Collect(context.Background(), metrics))
	diff := cmp.Diff(&metricdata.ResourceMetrics{
		Resource: metricResource(),
		ScopeMetrics: []metricdata.ScopeMetrics{
			{
				Scope: instrumentation.Scope{
					Name:    instrumentationName,
					Version: semanticVersion,
				},
				Metrics: []metricdata.Metrics{
					{
						Name: rpcClientDuration,
						Unit: unitMilliseconds,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   1000.0,
									Min:   metricdata.NewExtrema[int64](1000),
									Max:   metricdata.NewExtrema[int64](1000),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientRequestSize,
						Unit: unitBytes,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   size,
									Min:   metricdata.NewExtrema[int64](size),
									Max:   metricdata.NewExtrema[int64](size),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientResponseSize,
						Unit: unitBytes,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   size,
									Min:   metricdata.NewExtrema[int64](size),
									Max:   metricdata.NewExtrema[int64](size),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientRequestsPerRPC,
						Unit: unitDimensionless,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   1,
									Min:   metricdata.NewExtrema[int64](1),
									Max:   metricdata.NewExtrema[int64](1),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientResponsesPerRPC,
						Unit: unitDimensionless,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   1,
									Min:   metricdata.NewExtrema[int64](1),
									Max:   metricdata.NewExtrema[int64](1),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
				},
			},
		},
	},
		metrics,
		cmpOpts()...,
	)
	if diff != "" {
		t.Error(diff)
	}
}

func TestStreamingMetricsClientFail(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	var now time.Time
	interceptor, err := NewInterceptor(
		WithMeterProvider(meterProvider), optionFunc(func(c *config) {
			c.now = func() time.Time {
				now = now.Add(time.Second)
				return now
			}
		}),
	)
	require.NoError(t, err)
	connectClient, host, port := startServer(
		[]connect.HandlerOption{},
		[]connect.ClientOption{
			connect.WithInterceptors(interceptor),
		}, failPingServer())
	stream := connectClient.PingStream(context.Background())
	msg := &pingv1.PingStreamRequest{
		Data: []byte("Hello, otel!"),
	}
	size := int64(proto.Size(msg))
	require.NoError(t, stream.Send(msg))
	require.NoError(t, stream.CloseRequest())
	_, err = stream.Receive()
	require.Error(t, err)
	require.NoError(t, stream.CloseResponse())
	metrics := &metricdata.ResourceMetrics{}
	require.NoError(t, metricReader.Collect(context.Background(), metrics))
	diff := cmp.Diff(&metricdata.ResourceMetrics{
		Resource: metricResource(),
		ScopeMetrics: []metricdata.ScopeMetrics{
			{
				Scope: instrumentation.Scope{
					Name:    instrumentationName,
					Version: semanticVersion,
				},
				Metrics: []metricdata.Metrics{
					{
						Name: rpcClientDuration,
						Unit: string("ms"),
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
										attribute.Key(rpcBufConnectStatusCode).String("data_loss"),
									),
									Count: 1,
									Sum:   1000.0,
									Min:   metricdata.NewExtrema[int64](1000),
									Max:   metricdata.NewExtrema[int64](1000),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientRequestSize,
						Unit: unitBytes,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   size,
									Min:   metricdata.NewExtrema[int64](size),
									Max:   metricdata.NewExtrema[int64](size),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientResponseSize,
						Unit: unitBytes,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key(rpcBufConnectStatusCode).String("data_loss"),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   0.0,
									Min:   metricdata.NewExtrema[int64](0),
									Max:   metricdata.NewExtrema[int64](0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientRequestsPerRPC,
						Unit: unitDimensionless,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   1,
									Min:   metricdata.NewExtrema[int64](1),
									Max:   metricdata.NewExtrema[int64](1),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientResponsesPerRPC,
						Unit: unitDimensionless,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
										attribute.Key(rpcBufConnectStatusCode).String("data_loss"),
									),
									Count: 1,
									Sum:   1,
									Min:   metricdata.NewExtrema[int64](1),
									Max:   metricdata.NewExtrema[int64](1),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
				},
			},
		},
	},
		metrics,
		cmpOpts()...,
	)
	if diff != "" {
		t.Error(diff)
	}
}

func TestStreamingMetricsFail(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	var now time.Time
	interceptor, err := NewInterceptor(
		WithMeterProvider(meterProvider), optionFunc(func(c *config) {
			c.now = func() time.Time {
				now = now.Add(time.Second)
				return now
			}
		}),
	)
	require.NoError(t, err)
	connectClient, host, port := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(interceptor),
		}, []connect.ClientOption{}, failPingServer())
	stream := connectClient.PingStream(context.Background())
	msg := &pingv1.PingStreamRequest{
		Data: []byte("Hello, otel!"),
	}
	size := int64(proto.Size(msg))
	err = stream.Send(msg)
	require.NoError(t, err)
	require.NoError(t, stream.CloseRequest())
	_, err = stream.Receive()
	require.Error(t, err)
	require.NoError(t, stream.CloseResponse())
	metrics := &metricdata.ResourceMetrics{}
	require.NoError(t, metricReader.Collect(context.Background(), metrics))
	diff := cmp.Diff(&metricdata.ResourceMetrics{
		Resource: metricResource(),
		ScopeMetrics: []metricdata.ScopeMetrics{
			{
				Scope: instrumentation.Scope{
					Name:    instrumentationName,
					Version: semanticVersion,
				},
				Metrics: []metricdata.Metrics{
					{
						Name: rpcServerDuration,
						Unit: unitMilliseconds,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key(rpcBufConnectStatusCode).String("data_loss"),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   1000.0,
									Min:   metricdata.NewExtrema[int64](1000),
									Max:   metricdata.NewExtrema[int64](1000),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcServerRequestSize,
						Unit: unitBytes,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   size,
									Min:   metricdata.NewExtrema[int64](size),
									Max:   metricdata.NewExtrema[int64](size),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcServerRequestsPerRPC,
						Unit: unitDimensionless,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(PingStreamMethod),
									),
									Count: 1,
									Sum:   1,
									Min:   metricdata.NewExtrema[int64](1),
									Max:   metricdata.NewExtrema[int64](1),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
				},
			},
		},
	},
		metrics,
		cmpOpts()...,
	)
	if diff != "" {
		t.Error(diff)
	}
}

func TestMetrics(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	var now time.Time
	interceptor, err := NewInterceptor(
		WithMeterProvider(meterProvider),
		optionFunc(func(c *config) {
			c.now = func() time.Time {
				now = now.Add(time.Second)
				return now
			}
		}),
	)
	require.NoError(t, err)
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(interceptor),
	}, okayPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 12)); err != nil {
		t.Errorf(err.Error())
	}
	metrics := &metricdata.ResourceMetrics{}
	require.NoError(t, metricReader.Collect(context.Background(), metrics))
	diff := cmp.Diff(&metricdata.ResourceMetrics{
		Resource: metricResource(),
		ScopeMetrics: []metricdata.ScopeMetrics{
			{
				Scope: instrumentation.Scope{
					Name:    instrumentationName,
					Version: semanticVersion,
				},
				Metrics: []metricdata.Metrics{
					{
						Name: rpcClientDuration,
						Unit: unitMilliseconds,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCMethodKey.String(PingMethod),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCSystemKey.String(bufConnect),
									),
									Count: 1,
									Sum:   time.Second.Milliseconds(),
									Min:   metricdata.NewExtrema[int64](1000),
									Max:   metricdata.NewExtrema[int64](1000),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientRequestSize,
						Unit: unitBytes,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCMethodKey.String(PingMethod),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCSystemKey.String(bufConnect),
									),
									Count: 1,
									Sum:   16,
									Min:   metricdata.NewExtrema[int64](16),
									Max:   metricdata.NewExtrema[int64](16),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientResponseSize,
						Unit: unitBytes,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCMethodKey.String(PingMethod),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCSystemKey.String(bufConnect),
									),
									Count: 1,
									Sum:   16,
									Min:   metricdata.NewExtrema[int64](16),
									Max:   metricdata.NewExtrema[int64](16),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientRequestsPerRPC,
						Unit: unitDimensionless,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCMethodKey.String(PingMethod),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCSystemKey.String(bufConnect),
									),
									Count: 1,
									Sum:   1,
									Min:   metricdata.NewExtrema[int64](1),
									Max:   metricdata.NewExtrema[int64](1),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientResponsesPerRPC,
						Unit: unitDimensionless,
						Data: metricdata.Histogram[int64]{
							DataPoints: []metricdata.HistogramDataPoint[int64]{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCMethodKey.String(PingMethod),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCSystemKey.String(bufConnect),
									),
									Count: 1,
									Sum:   1,
									Min:   metricdata.NewExtrema[int64](1),
									Max:   metricdata.NewExtrema[int64](1),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
				},
			},
		},
	},
		metrics,
		cmpOpts()...,
	)
	if diff != "" {
		t.Error(diff)
	}
}

func TestWithoutMetrics(t *testing.T) {
	t.Parallel()
	metricReader := metricsdk.NewManualReader()
	meterProvider := metricsdk.NewMeterProvider(
		metricsdk.WithReader(
			metricReader,
		),
	)
	interceptor, err := NewInterceptor(WithMeterProvider(meterProvider), WithoutMetrics())
	require.NoError(t, err)
	pingClient, _, _ := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(interceptor),
	}, okayPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 12)); err != nil {
		t.Errorf(err.Error())
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
	interceptor, err := NewInterceptor(WithTracerProvider(traceProvider), WithoutTracing())
	require.NoError(t, err)
	pingClient, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(interceptor),
	}, nil, okayPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 0)); err != nil {
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
	interceptor, err := NewInterceptor(WithTracerProvider(clientTraceProvider))
	assert.NoError(t, err)
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(interceptor),
	}, okayPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}
	require.Len(t, clientSpanRecorder.Ended(), 1)
	require.Equal(t, clientSpanRecorder.Ended()[0].Status().Code, codes.Unset)
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + PingMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(PingMethod),
			},
		},
	}, clientSpanRecorder.Ended())
}

func TestHandlerFailCall(t *testing.T) {
	t.Parallel()
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	interceptor, err := NewInterceptor(WithTracerProvider(clientTraceProvider))
	require.NoError(t, err)
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(interceptor),
	}, okayPingServer())
	_, err = pingClient.Fail(
		context.Background(),
		connect.NewRequest(&pingv1.FailRequest{Code: int32(connect.CodeInternal)}),
	)
	require.Error(t, err)
	require.Len(t, clientSpanRecorder.Ended(), 1)
	require.Equal(t, clientSpanRecorder.Ended()[0].Status().Code, codes.Error)
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + FailMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(0),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(FailMethod),
				attribute.Key(rpcBufConnectStatusCode).String(UnimplementedString),
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
	serverInterceptor, err := NewInterceptor(
		WithTracerProvider(serverTraceProvider),
		WithFilter(func(ctx context.Context, request *Request) bool {
			return false
		}),
	)
	require.NoError(t, err)
	clientInterceptor, err := NewInterceptor(
		WithTracerProvider(clientTraceProvider),
	)
	require.NoError(t, err)
	pingClient, host, port := startServer([]connect.HandlerOption{
		connect.WithInterceptors(serverInterceptor),
	}, []connect.ClientOption{
		connect.WithInterceptors(clientInterceptor),
	}, okayPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}
	assertSpans(t, []wantSpans{}, serverSpanRecorder.Ended())
	require.Len(t, clientSpanRecorder.Ended(), 1)
	require.Equal(t, clientSpanRecorder.Ended()[0].Status().Code, codes.Unset)
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + PingMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(PingMethod),
			},
		},
	}, clientSpanRecorder.Ended())
}

func TestBasicFilter(t *testing.T) {
	t.Parallel()
	headerKey, headerVal := "Some-Header", "foobar"
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	serverInterceptor, err := NewInterceptor(WithTracerProvider(traceProvider), WithFilter(func(ctx context.Context, request *Request) bool {
		return false
	}))
	require.NoError(t, err)
	pingClient, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(serverInterceptor),
	}, nil, okayPingServer())
	req := requestOfSize(1, 0)
	req.Header().Set(headerKey, headerVal)
	if _, err := pingClient.Ping(context.Background(), req); err != nil {
		t.Errorf(err.Error())
	}
	if len(spanRecorder.Ended()) != 0 {
		t.Error("unexpected spans recorded")
	}
	assertSpans(t, []wantSpans{}, spanRecorder.Ended())
}

func TestFilterHeader(t *testing.T) {
	t.Parallel()
	headerKey, headerVal := "Some-Header", "foobar"
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	serverInterceptor, err := NewInterceptor(WithTracerProvider(traceProvider), WithFilter(func(ctx context.Context, request *Request) bool {
		return request.Header.Get(headerKey) == headerVal
	}))
	require.NoError(t, err)
	pingClient, host, port := startServer([]connect.HandlerOption{
		connect.WithInterceptors(serverInterceptor),
	}, nil, okayPingServer())
	req := requestOfSize(1, 0)
	req.Header().Set(headerKey, headerVal)
	if _, err := pingClient.Ping(context.Background(), req); err != nil {
		t.Errorf(err.Error())
	}
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + PingMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(PingMethod),
			},
		},
	}, spanRecorder.Ended())
}

func TestHeaderAttribute(t *testing.T) {
	t.Parallel()
	var propagator propagation.TraceContext
	handlerSpanRecorder := tracetest.NewSpanRecorder()
	handlerTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(handlerSpanRecorder))
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	pingReq, pingRes, cumsumReq, cumsumRes := "pingReq", "pingRes", "cumsumReq", "cumsumRes"
	pingReqKey := "rpc.connect_rpc.request.metadata.pingreq"
	pingResKey := "rpc.connect_rpc.response.metadata.pingres"
	cumsumReqKey := "rpc.connect_rpc.request.metadata.cumsumreq"
	cumsumResKey := "rpc.connect_rpc.response.metadata.cumsumres"
	value := "value"
	attributeValue := []string{value}
	attributeValueLong := []string{value, value}
	attributePingReq := attribute.StringSlice(pingReqKey, attributeValue)
	attributeCumsumReq := attribute.StringSlice(cumsumReqKey, attributeValue)
	attributePingRes := attribute.StringSlice(pingResKey, attributeValueLong)
	attributeCumsumRes := attribute.StringSlice(cumsumResKey, attributeValue)
	requestHeaderOption := WithTraceRequestHeader(pingReq, cumsumReq)
	responseHeaderOption := WithTraceResponseHeader(pingRes, cumsumRes)
	serverInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(handlerTraceProvider),
		requestHeaderOption,
		responseHeaderOption,
	)
	require.NoError(t, err)
	clientInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(clientTraceProvider),
		requestHeaderOption,
		responseHeaderOption,
	)
	require.NoError(t, err)
	client, _, _ := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(serverInterceptor)},
		[]connect.ClientOption{
			connect.WithInterceptors(clientInterceptor),
		}, &pluggablePingServer{
			ping: func(ctx context.Context, req *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
				response := connect.NewResponse(&pingv1.PingResponse{})
				response.Header().Set(pingRes, value)
				response.Header().Add(pingRes, value) // Add two values to test formatting
				return response, nil
			},
			pingStream: func(ctx context.Context, stream *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse]) error {
				stream.ResponseHeader().Set(cumsumRes, value)
				_, _ = stream.Receive()
				assert.NoError(t, stream.Send(&pingv1.PingStreamResponse{}))
				return nil
			},
		})
	pingRequest := connect.NewRequest(&pingv1.PingRequest{Id: 1})
	// Set request metadata for unary ping request
	pingRequest.Header().Set(pingReq, value)
	_, err = client.Ping(context.Background(), pingRequest)
	assert.NoError(t, err)
	stream := client.PingStream(context.Background())
	// Set request metadata for streaming cumsum
	stream.RequestHeader().Set(cumsumReq, value)
	assert.NoError(t, stream.Send(&pingv1.PingStreamRequest{}))
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	assert.Equal(t, len(handlerSpanRecorder.Ended()), 2)
	assert.Equal(t, len(clientSpanRecorder.Ended()), 2)
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
}

func TestInterceptors(t *testing.T) {
	t.Parallel()
	const largeMessageSize = 1000
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	serverInterceptor, err := NewInterceptor(WithTracerProvider(traceProvider))
	require.NoError(t, err)
	pingClient, host, port := startServer([]connect.HandlerOption{
		connect.WithInterceptors(serverInterceptor),
	}, nil, okayPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}
	if _, err := pingClient.Ping(context.Background(), requestOfSize(2, largeMessageSize)); err != nil {
		t.Errorf(err.Error())
	}
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + PingMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(PingMethod),
			},
		},
		{
			spanName: pingv1connect.PingServiceName + "/" + PingMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(1005),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(1005),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(PingMethod),
			},
		},
	}, spanRecorder.Ended())
}

func TestUnaryHandlerNoTraceParent(t *testing.T) {
	t.Parallel()
	assertTraceParent := func(ctx context.Context, req *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
		assert.Zero(t, req.Header().Get(TraceParentKey))
		return connect.NewResponse(&pingv1.PingResponse{Id: req.Msg.Id}), nil
	}
	serverInterceptor, err := NewInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(trace.NewTracerProvider()),
	)
	require.NoError(t, err)
	client, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(serverInterceptor),
	}, nil, &pluggablePingServer{ping: assertTraceParent})
	resp, err := client.Ping(context.Background(), connect.NewRequest(&pingv1.PingRequest{Id: 1}))
	assert.NoError(t, err)
	assert.Equal(t, int64(1), resp.Msg.Id)
}

func TestStreamingHandlerNoTraceParent(t *testing.T) {
	t.Parallel()
	msg := &pingv1.PingStreamResponse{
		Data: []byte("Hello, otel!"),
	}
	assertTraceParent := func(ctx context.Context, stream *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse]) error {
		assert.Zero(t, stream.RequestHeader().Get(TraceParentKey))
		assert.NoError(t, stream.Send(msg))
		return nil
	}
	serverInterceptor, err := NewInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(trace.NewTracerProvider()),
	)
	require.NoError(t, err)
	client, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(serverInterceptor),
	}, nil, &pluggablePingServer{pingStream: assertTraceParent},
	)
	stream := client.PingStream(context.Background())
	assert.NoError(t, stream.CloseRequest())
	resp, err := stream.Receive()
	assert.NoError(t, stream.CloseResponse())
	assert.NoError(t, err)
	assert.Equal(t, msg.Data, resp.Data)
}

func TestPropagationBaggage(t *testing.T) {
	t.Parallel()
	propagator := propagation.NewCompositeTextMapPropagator(propagation.Baggage{}, propagation.TraceContext{})
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	assertBaggage := connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			assert.Equal(t, "foo=bar", request.Header().Get("Baggage"))
			return next(ctx, request)
		}
	}))
	serverInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(traceProvider),
		WithTrustRemote())
	require.NoError(t, err)
	clientInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(traceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(serverInterceptor),
			assertBaggage,
		}, []connect.ClientOption{
			connect.WithInterceptors(clientInterceptor),
			assertBaggage,
		}, okayPingServer())
	bag, _ := baggage.Parse("foo=bar")
	ctx := baggage.ContextWithBaggage(context.Background(), bag)
	_, err = client.Ping(ctx, connect.NewRequest(&pingv1.PingRequest{Id: 1}))
	assert.NoError(t, err)
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
	serverInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(handlerTraceProvider),
		WithTrustRemote(),
	)
	require.NoError(t, err)
	clientInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(clientTraceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(serverInterceptor),
		}, []connect.ClientOption{
			connect.WithInterceptors(clientInterceptor),
		}, okayPingServer())
	_, err = client.Ping(ctx, connect.NewRequest(&pingv1.PingRequest{Id: 1}))
	assert.NoError(t, err)
	assert.Equal(t, len(handlerSpanRecorder.Ended()), 1)
	assert.Equal(t, len(clientSpanRecorder.Ended()), 1)
	assertSpanParent(t, rootSpan, clientSpanRecorder.Ended()[0], handlerSpanRecorder.Ended()[0])
}

func TestUnaryInterceptorPropagation(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	var ctx context.Context
	var span traceapi.Span
	clientInterceptor, err := NewInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(traceProvider),
		WithTrustRemote(),
	)
	require.NoError(t, err)
	client, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(connect.UnaryInterceptorFunc(func(unaryFunc connect.UnaryFunc) connect.UnaryFunc {
			return func(_ context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
				ctx, span = trace.NewTracerProvider().Tracer("test").Start(context.Background(), "test")
				return unaryFunc(ctx, request)
			}
		})),
		connect.WithInterceptors(clientInterceptor),
	}, nil, okayPingServer())
	resp, err := client.Ping(context.Background(), connect.NewRequest(&pingv1.PingRequest{Id: 1}))
	assert.NoError(t, err)
	assert.Equal(t, int64(1), resp.Msg.Id)
	assert.Equal(t, len(spanRecorder.Ended()), 1)
	recordedSpan := spanRecorder.Ended()[0]
	assert.True(t, recordedSpan.Parent().IsValid())
	assert.True(t, recordedSpan.Parent().Equal(span.SpanContext()))
}

func TestUnaryInterceptorNotModifiedError(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	var ctx context.Context
	serverInterceptor, err := NewInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(traceProvider),
		WithTrustRemote(),
	)
	require.NoError(t, err)
	client, _, _ := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(connect.UnaryInterceptorFunc(func(unaryFunc connect.UnaryFunc) connect.UnaryFunc {
				return func(_ context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
					ctx, _ = trace.NewTracerProvider().Tracer("test").Start(context.Background(), "test")
					return unaryFunc(ctx, request)
				}
			})),
			connect.WithInterceptors(serverInterceptor),
		},
		[]connect.ClientOption{
			connect.WithHTTPGet(),
		},
		okayPingServer(),
	)
	req := connect.NewRequest(&pingv1.PingRequest{Id: 1})
	req.Header().Set("If-None-Match", cacheablePingEtag)
	_, err = client.Ping(context.Background(), req)
	assert.ErrorContains(t, err, "not modified")
	assert.True(t, connect.IsNotModifiedError(err))
	assert.Equal(t, len(spanRecorder.Ended()), 1)
	recordedSpan := spanRecorder.Ended()[0]
	assert.Equal(t, codes.Unset, recordedSpan.Status().Code)
	var codeAttributes []attribute.KeyValue
	for _, attr := range recordedSpan.Attributes() {
		if attr.Key == semconv.HTTPStatusCodeKey {
			codeAttributes = append(codeAttributes, attr)
		} else if strings.HasPrefix(string(attr.Key), "rpc") && strings.HasSuffix(string(attr.Key), "code") {
			codeAttributes = append(codeAttributes, attr)
		}
	}
	// should not be any RPC status attribute, only the HTTP status attribute
	expectedCodeAttributes := []attribute.KeyValue{semconv.HTTPStatusCodeKey.Int(304)}
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
	serverInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(handlerTraceProvider),
	)
	require.NoError(t, err)
	clientInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(clientTraceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(serverInterceptor),
		}, []connect.ClientOption{
			connect.WithInterceptors(clientInterceptor),
		}, okayPingServer())
	_, err = client.Ping(ctx, connect.NewRequest(&pingv1.PingRequest{Id: 1}))
	assert.NoError(t, err)
	assert.Equal(t, len(handlerSpanRecorder.Ended()), 1)
	assert.Equal(t, len(clientSpanRecorder.Ended()), 1)
	assertSpanLink(t, rootSpan, clientSpanRecorder.Ended()[0], handlerSpanRecorder.Ended()[0])
}

func TestStreamingHandlerInterceptorPropagation(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	var span traceapi.Span
	ctx := context.Background()
	serverInterceptor, err := NewInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(traceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(streamingHandlerInterceptorFunc(func(handlerFunc connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
			return func(_ context.Context, conn connect.StreamingHandlerConn) error {
				ctx, span = trace.NewTracerProvider().Tracer("test").Start(ctx, "test")
				return handlerFunc(ctx, conn)
			}
		})),
		connect.WithInterceptors(serverInterceptor),
	}, nil, okayPingServer(),
	)
	stream := client.PingStream(context.Background())
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	assert.Equal(t, len(spanRecorder.Ended()), 1)
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
	serverInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(handlerTraceProvider),
		WithTrustRemote(),
	)
	assert.NoError(t, err)
	clientInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(clientTraceProvider),
	)
	assert.NoError(t, err)
	client, _, _ := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(serverInterceptor),
		}, []connect.ClientOption{
			connect.WithInterceptors(clientInterceptor),
		}, okayPingServer())
	stream := client.PingStream(ctx)
	assert.NoError(t, stream.Send(nil))
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	assert.Equal(t, len(handlerSpanRecorder.Ended()), 1)
	assert.Equal(t, len(clientSpanRecorder.Ended()), 1)
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
	serverInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(handlerTraceProvider),
	)
	require.NoError(t, err)
	clientInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(clientTraceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(serverInterceptor),
		}, []connect.ClientOption{
			connect.WithInterceptors(clientInterceptor),
		}, okayPingServer())
	stream := client.PingStream(ctx)
	assert.NoError(t, stream.Send(nil))
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	assert.Equal(t, len(handlerSpanRecorder.Ended()), 1)
	assert.Equal(t, len(clientSpanRecorder.Ended()), 1)
	assertSpanLink(t, rootSpan, clientSpanRecorder.Ended()[0], handlerSpanRecorder.Ended()[0])
}

func TestStreamingClientPropagation(t *testing.T) {
	t.Parallel()
	msg := &pingv1.PingStreamResponse{
		Data: []byte("Hello, otel!"),
	}
	assertTraceParent := func(ctx context.Context, stream *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse]) error {
		assert.NotZero(t, stream.RequestHeader().Get(TraceParentKey))
		assert.NoError(t, stream.Send(msg))
		return nil
	}
	clientInterceptor, err := NewInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(trace.NewTracerProvider()),
	)
	require.NoError(t, err)
	client, _, _ := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(clientInterceptor),
	}, &pluggablePingServer{pingStream: assertTraceParent},
	)
	stream := client.PingStream(context.Background())
	assert.NoError(t, stream.Send(nil))
	assert.NoError(t, stream.CloseRequest())
	resp, err := stream.Receive()
	assert.NoError(t, stream.CloseResponse())
	assert.NoError(t, err)
	assert.Equal(t, msg.Data, resp.Data)
}

func TestStreamingHandlerTracing(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	serverInterceptor, err := NewInterceptor(WithTracerProvider(traceProvider))
	require.NoError(t, err)
	pingClient, host, port := startServer([]connect.HandlerOption{
		connect.WithInterceptors(serverInterceptor),
	}, nil, okayPingServer())
	stream := pingClient.PingStream(context.Background())

	msg := &pingv1.PingStreamRequest{
		Data: []byte("Hello, otel!"),
	}
	size := proto.Size(msg)
	assert.NoError(t, stream.Send(msg))
	_, err = stream.Receive()
	assert.NoError(t, err)
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	require.Len(t, spanRecorder.Ended(), 1)
	require.Equal(t, spanRecorder.Ended()[0].Status().Code, codes.Unset)
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + PingStreamMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageUncompressedSizeKey.Int(size),
						semconv.MessageIDKey.Int(1),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageUncompressedSizeKey.Int(size),
						semconv.MessageIDKey.Int(1),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(PingStreamMethod),
			},
		},
	}, spanRecorder.Ended())
}

func TestStreamingClientTracing(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	clientInterceptor, err := NewInterceptor(WithTracerProvider(traceProvider))
	require.NoError(t, err)
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(clientInterceptor),
	}, okayPingServer())
	stream := pingClient.PingStream(context.Background())

	msg := &pingv1.PingStreamRequest{Data: []byte("Hello, otel!")}
	size := proto.Size(msg)
	assert.NoError(t, stream.Send(msg))
	_, err = stream.Receive()
	assert.NoError(t, err)
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	require.Len(t, spanRecorder.Ended(), 1)
	require.Equal(t, spanRecorder.Ended()[0].Status().Code, codes.Unset)
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + PingStreamMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageUncompressedSizeKey.Int(size),
						semconv.MessageIDKey.Int(1),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageUncompressedSizeKey.Int(size),
						semconv.MessageIDKey.Int(1),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(PingStreamMethod),
			},
		},
	}, spanRecorder.Ended())
}

func TestWithAttributeFilter(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	clientInterceptor, err := NewInterceptor(
		WithTracerProvider(traceProvider),
		WithAttributeFilter(func(_ *Request, value attribute.KeyValue) bool {
			if value.Key == semconv.MessageIDKey {
				return false
			}
			if value.Key == semconv.RPCServiceKey {
				return false
			}
			return true
		},
		),
	)
	require.NoError(t, err)
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(clientInterceptor),
	}, okayPingServer())
	stream := pingClient.PingStream(context.Background())

	msg := &pingv1.PingStreamRequest{Data: []byte("Hello, otel!")}
	size := proto.Size(msg)
	assert.NoError(t, stream.Send(msg))
	_, err = stream.Receive()
	assert.NoError(t, err)
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + PingStreamMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageUncompressedSizeKey.Int(size),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageUncompressedSizeKey.Int(size),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCMethodKey.String(PingStreamMethod),
			},
		},
	}, spanRecorder.Ended())
}

func TestWithoutServerPeerAttributes(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	serverInterceptor, err := NewInterceptor(
		WithTracerProvider(traceProvider),
		WithoutServerPeerAttributes(),
	)
	require.NoError(t, err)
	pingClient, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(serverInterceptor),
	}, nil, okayPingServer())
	stream := pingClient.PingStream(context.Background())
	msg := &pingv1.PingStreamRequest{Data: []byte("Hello, otel!")}
	size := proto.Size(msg)
	assert.NoError(t, stream.Send(msg))
	_, err = stream.Receive()
	assert.NoError(t, err)
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + PingStreamMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageUncompressedSizeKey.Int(size),
						semconv.MessageIDKey.Int(1),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageUncompressedSizeKey.Int(size),
						semconv.MessageIDKey.Int(1),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(PingStreamMethod),
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
	serverInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(handlerTraceProvider),
	)
	require.NoError(t, err)
	clientInterceptor, err := NewInterceptor(
		WithPropagator(propagator),
		WithTracerProvider(clientTraceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(serverInterceptor),
		}, []connect.ClientOption{
			connect.WithInterceptors(clientInterceptor),
		}, failPingServer())
	stream := client.PingStream(context.Background())
	assert.NoError(t, stream.Send(&pingv1.PingStreamRequest{
		Data: []byte("Hello, otel!"),
	}))
	_, err = stream.Receive()
	assert.Error(t, err)
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	assert.Equal(t, len(handlerSpanRecorder.Ended()), 1)
	assert.Equal(t, len(clientSpanRecorder.Ended()), 1)
	assert.Equal(t, handlerSpanRecorder.Ended()[0].Status().Code, codes.Error)
	assert.Equal(t, clientSpanRecorder.Ended()[0].Status().Code, codes.Error)
}

func TestWithoutTraceEventsStreaming(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	serverInterceptor, err := NewInterceptor(
		WithTracerProvider(traceProvider),
		WithoutTraceEvents(),
	)
	require.NoError(t, err)
	pingClient, host, port := startServer([]connect.HandlerOption{
		connect.WithInterceptors(serverInterceptor),
	}, nil, okayPingServer())
	stream := pingClient.PingStream(context.Background())
	assert.NoError(t, stream.Send(&pingv1.PingStreamRequest{
		Data: []byte("Hello, otel!"),
	}))
	_, err = stream.Receive()
	assert.NoError(t, err)
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + PingStreamMethod,
			events:   []trace.Event{},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(PingStreamMethod),
			},
		},
	}, spanRecorder.Ended())
}

func TestWithoutTraceEventsUnary(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	serverInterceptor, err := NewInterceptor(
		WithTracerProvider(traceProvider),
		WithoutTraceEvents(),
	)
	require.NoError(t, err)
	pingClient, host, port := startServer([]connect.HandlerOption{
		connect.WithInterceptors(serverInterceptor),
	}, nil, okayPingServer())
	_, err = pingClient.Ping(context.Background(), connect.NewRequest(&pingv1.PingRequest{Id: 1}))
	assert.NoError(t, err)
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + PingMethod,
			events:   []trace.Event{},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(PingMethod),
			},
		},
	}, spanRecorder.Ended())
}

// streamingHandlerInterceptorFunc is a simple Interceptor implementation that only
// wraps streaming handler RPCs. It has no effect on unary or streaming client RPCs.
type streamingHandlerInterceptorFunc func(connect.StreamingHandlerFunc) connect.StreamingHandlerFunc

// WrapUnary implements [Interceptor] with a no-op.
func (f streamingHandlerInterceptorFunc) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return next
}

// WrapStreamingClient implements [Interceptor] with a no-op.
func (f streamingHandlerInterceptorFunc) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements [Interceptor] by applying the interceptor function.
func (f streamingHandlerInterceptorFunc) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return f(next)
}

type wantSpans struct {
	spanName string
	events   []trace.Event
	attrs    []attribute.KeyValue
}

func assertSpans(t *testing.T, want []wantSpans, got []trace.ReadOnlySpan) {
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
			t.Fatal("event lengths do not match")
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
		diff := cmp.Diff(wantAttributes, span.Attributes(),
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

func startServer(handlerOpts []connect.HandlerOption, clientOpts []connect.ClientOption, svc pingv1connect.PingServiceHandler) (pingv1connect.PingServiceClient, string, int) {
	mux := http.NewServeMux()
	mux.Handle(pingv1connect.NewPingServiceHandler(svc, handlerOpts...))
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	pingClient := pingv1connect.NewPingServiceClient(server.Client(), server.URL, clientOpts...)
	host, port, _ := net.SplitHostPort(strings.ReplaceAll(server.URL, "https://", ""))
	portint, _ := strconv.Atoi(port)
	return pingClient, host, portint
}

func requestOfSize(id, dataSize int64) *connect.Request[pingv1.PingRequest] {
	body := make([]byte, dataSize)
	for i := range body {
		body[i] = byte(rand.Intn(128)) //nolint: gosec
	}
	return connect.NewRequest(&pingv1.PingRequest{Id: id, Data: body})
}

type optionFunc func(*config)

func (o optionFunc) apply(c *config) {
	o(c)
}

func cmpOpts() []cmp.Option {
	return []cmp.Option{
		cmp.Comparer(func(setx, sety attribute.Set) bool {
			setx, _ = setx.Filter(func(value attribute.KeyValue) bool {
				return value.Key != semconv.NetPeerPortKey
			})
			sety, _ = sety.Filter(func(value attribute.KeyValue) bool {
				return value.Key != semconv.NetPeerPortKey
			})
			return setx.Equals(&sety)
		}),
		cmp.Comparer(func(extx, exty metricdata.Extrema[int64]) bool {
			valx, definedx := extx.Value()
			valy, definedy := exty.Value()
			return valx == valy && definedx == definedy
		}),
		cmpopts.SortSlices(func(x, y metricdata.HistogramDataPoint[int64]) bool {
			return x.Attributes.Len() > y.Attributes.Len()
		}),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint[int64]{}, "StartTime"),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint[int64]{}, "Time"),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint[int64]{}, "Bounds"),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint[int64]{}, "BucketCounts"),
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

func metricResource() *resource.Resource {
	return resource.NewWithAttributes("https://opentelemetry.io/schemas/1.12.0",
		attribute.String("service.name", "test"),
		attribute.String("telemetry.sdk.language", "go"),
		attribute.String("telemetry.sdk.name", "opentelemetry"),
		attribute.String("telemetry.sdk.version", otel.Version()),
	)
}
