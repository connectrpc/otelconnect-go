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
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
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
	TraceParentKey           = "Traceparent"
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
						Name:        rpcServerDuration,
						Description: durationDesc,
						Unit:        unitMilliseconds,
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
						Name:        rpcServerRequestSize,
						Description: requestSizeDesc,
						Unit:        unitBytes,
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
						Name:        rpcServerResponseSize,
						Description: responseSizeDesc,
						Unit:        unitBytes,
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
						Name:        rpcServerRequestsPerRPC,
						Description: requestsPerRPCDesc,
						Unit:        unitDimensionless,
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
						Name:        rpcServerResponsesPerRPC,
						Description: responsesPerRPCDesc,
						Unit:        unitDimensionless,
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
	assert.Empty(t, diff)
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
						Name:        rpcClientDuration,
						Description: durationDesc,
						Unit:        unitMilliseconds,
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
						Name:        rpcClientRequestSize,
						Description: requestSizeDesc,
						Unit:        unitBytes,
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
						Name:        rpcClientResponseSize,
						Description: responseSizeDesc,
						Unit:        unitBytes,
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
						Name:        rpcClientRequestsPerRPC,
						Description: requestsPerRPCDesc,
						Unit:        unitDimensionless,
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
						Name:        rpcClientResponsesPerRPC,
						Description: responsesPerRPCDesc,
						Unit:        unitDimensionless,
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
						Name:        rpcClientDuration,
						Description: durationDesc,
						Unit:        "ms",
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
						Name:        rpcClientRequestSize,
						Description: requestSizeDesc,
						Unit:        unitBytes,
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
						Name:        rpcClientResponseSize,
						Description: responseSizeDesc,
						Unit:        unitBytes,
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
										attribute.Key(rpcBufConnectStatusCode).String("data_loss"),
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
						Name:        rpcClientRequestsPerRPC,
						Description: requestsPerRPCDesc,
						Unit:        unitDimensionless,
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
					{
						Name:        rpcClientResponsesPerRPC,
						Description: responsesPerRPCDesc,
						Unit:        unitDimensionless,
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
						Name:        rpcServerDuration,
						Description: durationDesc,
						Unit:        unitMilliseconds,
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
						Name:        rpcServerRequestSize,
						Description: requestSizeDesc,
						Unit:        unitBytes,
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
						Name:        rpcServerRequestsPerRPC,
						Description: requestsPerRPCDesc,
						Unit:        unitDimensionless,
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
					{
						Name:        rpcServerResponsesPerRPC,
						Description: responsesPerRPCDesc,
						Unit:        unitDimensionless,
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
									Sum:   0,
									Min:   metricdata.NewExtrema[int64](0),
									Max:   metricdata.NewExtrema[int64](0),
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
		t.Error(err)
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
						Name:        rpcClientDuration,
						Description: durationDesc,
						Unit:        unitMilliseconds,
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
						Name:        rpcClientRequestSize,
						Description: requestSizeDesc,
						Unit:        unitBytes,
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
						Name:        rpcClientResponseSize,
						Description: responseSizeDesc,
						Unit:        unitBytes,
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
						Name:        rpcClientRequestsPerRPC,
						Description: requestsPerRPCDesc,
						Unit:        unitDimensionless,
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
						Name:        rpcClientResponsesPerRPC,
						Description: responsesPerRPCDesc,
						Unit:        unitDimensionless,
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
	interceptor, err := NewInterceptor(WithTracerProvider(traceProvider), WithoutTracing())
	require.NoError(t, err)
	pingClient, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(interceptor),
	}, nil, okayPingServer())
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
	interceptor, err := NewInterceptor(WithTracerProvider(clientTraceProvider))
	require.NoError(t, err)
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(interceptor),
	}, okayPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 0)); err != nil {
		t.Error(err)
	}
	require.Len(t, clientSpanRecorder.Ended(), 1)
	require.Equal(t, codes.Unset, clientSpanRecorder.Ended()[0].Status().Code)
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
	require.Equal(t, codes.Error, clientSpanRecorder.Ended()[0].Status().Code)
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
		WithFilter(func(_ context.Context, _ connect.Spec) bool {
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
		t.Error(err)
	}
	assertSpans(t, []wantSpans{}, serverSpanRecorder.Ended())
	require.Len(t, clientSpanRecorder.Ended(), 1)
	require.Equal(t, codes.Unset, clientSpanRecorder.Ended()[0].Status().Code)
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
	serverInterceptor, err := NewInterceptor(WithTracerProvider(traceProvider), WithFilter(func(_ context.Context, _ connect.Spec) bool {
		return false
	}))
	require.NoError(t, err)
	pingClient, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(serverInterceptor),
	}, nil, okayPingServer())
	req := requestOfSize(1, 0)
	req.Header().Set(headerKey, headerVal)
	if _, err := pingClient.Ping(context.Background(), req); err != nil {
		t.Error(err)
	}
	if len(spanRecorder.Ended()) != 0 {
		t.Error("unexpected spans recorded")
	}
	assertSpans(t, []wantSpans{}, spanRecorder.Ended())
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
			connect.WithInterceptors(serverInterceptor),
		},
		[]connect.ClientOption{
			connect.WithInterceptors(clientInterceptor),
		}, &pluggablePingServer{
			ping: func(_ context.Context, _ *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
				response := connect.NewResponse(&pingv1.PingResponse{})
				response.Header().Set(pingRes, value)
				response.Header().Add(pingRes, value) // Add two values to test formatting
				return response, nil
			},
			pingStream: func(_ context.Context, stream *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse]) error {
				stream.ResponseHeader().Set(cumsumRes, value)
				_, _ = stream.Receive()
				return stream.Send(&pingv1.PingStreamResponse{})
			},
		})
	pingRequest := connect.NewRequest(&pingv1.PingRequest{Id: 1})
	// Set request metadata for unary ping request
	pingRequest.Header().Set(pingReq, value)
	_, err = client.Ping(context.Background(), pingRequest)
	require.NoError(t, err)
	stream := client.PingStream(context.Background())
	// Set request metadata for streaming cumsum
	stream.RequestHeader().Set(cumsumReq, value)
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{}))
	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
	assert.Len(t, handlerSpanRecorder.Ended(), 2)
	assert.Len(t, clientSpanRecorder.Ended(), 2)
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
		t.Error(err)
	}
	if _, err := pingClient.Ping(context.Background(), requestOfSize(2, largeMessageSize)); err != nil {
		t.Error(err)
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
	assertTraceParent := func(_ context.Context, req *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
		assert.Zero(t, req.Header().Get(TraceParentKey))
		return connect.NewResponse(&pingv1.PingResponse{Id: req.Msg.GetId()}), nil
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
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Msg.GetId())
}

func TestStreamingHandlerNoTraceParent(t *testing.T) {
	t.Parallel()
	msg := &pingv1.PingStreamResponse{
		Data: []byte("Hello, otel!"),
	}
	assertTraceParent := func(_ context.Context, stream *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse]) error {
		assert.Zero(t, stream.RequestHeader().Get(TraceParentKey))
		return stream.Send(msg)
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
	require.NoError(t, stream.CloseRequest())
	resp, err := stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseResponse())
	assert.Equal(t, msg.GetData(), resp.GetData())
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
			connect.WithInterceptors(serverInterceptor, assertSpanInterceptor{t: t}),
		}, []connect.ClientOption{
			connect.WithInterceptors(clientInterceptor, assertSpanInterceptor{t: t}),
		}, okayPingServer())
	_, err = client.Ping(ctx, connect.NewRequest(&pingv1.PingRequest{Id: 1}))
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
	clientInterceptor, err := NewInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(traceProvider),
		WithTrustRemote(),
	)
	require.NoError(t, err)
	client, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(connect.UnaryInterceptorFunc(func(unaryFunc connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
				ctx, span = trace.NewTracerProvider().Tracer("test").Start(ctx, "test")
				return unaryFunc(ctx, request)
			}
		})),
		connect.WithInterceptors(clientInterceptor),
	}, nil, okayPingServer())
	resp, err := client.Ping(context.Background(), connect.NewRequest(&pingv1.PingRequest{Id: 1}))
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Msg.GetId())
	assert.Len(t, spanRecorder.Ended(), 1)
	recordedSpan := spanRecorder.Ended()[0]
	assert.True(t, recordedSpan.Parent().IsValid())
	assert.True(t, recordedSpan.Parent().Equal(span.SpanContext()))
}

func TestUnaryInterceptorNotModifiedError(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	serverInterceptor, err := NewInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(traceProvider),
		WithTrustRemote(),
	)
	require.NoError(t, err)
	client, _, _ := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(connect.UnaryInterceptorFunc(func(unaryFunc connect.UnaryFunc) connect.UnaryFunc {
				return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
					ctx, span := trace.NewTracerProvider().Tracer("test").Start(ctx, "test")
					defer span.End()
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
	require.ErrorContains(t, err, "not modified")
	assert.True(t, connect.IsNotModifiedError(err))
	assert.Len(t, spanRecorder.Ended(), 1)
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
	serverInterceptor, err := NewInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(traceProvider),
	)
	require.NoError(t, err)
	client, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(streamingHandlerInterceptorFunc(func(handlerFunc connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
			return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
				ctx, span = trace.NewTracerProvider().Tracer("test").Start(ctx, "test")
				return handlerFunc(ctx, conn)
			}
		})),
		connect.WithInterceptors(serverInterceptor),
	}, nil, okayPingServer(),
	)
	stream := client.PingStream(context.Background())
	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
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
	stream := client.PingStream(ctx)
	require.NoError(t, stream.Send(nil))
	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
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
	require.NoError(t, stream.Send(nil))
	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
	assert.Len(t, handlerSpanRecorder.Ended(), 1)
	assert.Len(t, clientSpanRecorder.Ended(), 1)
	assertSpanLink(t, rootSpan, clientSpanRecorder.Ended()[0], handlerSpanRecorder.Ended()[0])
}

func TestStreamingClientPropagation(t *testing.T) {
	t.Parallel()
	msg := &pingv1.PingStreamResponse{
		Data: []byte("Hello, otel!"),
	}
	assertTraceParent := func(_ context.Context, stream *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse]) error {
		assert.NotZero(t, stream.RequestHeader().Get(TraceParentKey))
		require.NoError(t, stream.Send(msg))
		return nil
	}
	clientInterceptor, err := NewInterceptor(
		WithPropagator(propagation.TraceContext{}),
		WithTracerProvider(trace.NewTracerProvider()),
	)
	require.NoError(t, err)
	client, _, _ := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(clientInterceptor, assertSpanInterceptor{t: t}),
	}, &pluggablePingServer{pingStream: assertTraceParent},
	)
	stream := client.PingStream(context.Background())
	require.NoError(t, stream.Send(nil))
	require.NoError(t, stream.CloseRequest())
	resp, err := stream.Receive()
	require.NoError(t, stream.CloseResponse())
	require.NoError(t, err)
	assert.Equal(t, msg.GetData(), resp.GetData())
}

func TestStreamingClientContextCancellation(t *testing.T) {
	t.Parallel()
	msg := &pingv1.PingStreamResponse{
		Data: []byte("Hello, otel!"),
	}
	server := &pluggablePingServer{
		pingStream: func(_ context.Context, stream *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse]) error {
			require.NoError(t, stream.Send(msg))
			return errors.New("stream closed") // Simulate error in stream.
		},
	}
	clientInterceptor, err := NewInterceptor()
	require.NoError(t, err)
	client, _, _ := startServer(
		nil,
		[]connect.ClientOption{connect.WithInterceptors(clientInterceptor)},
		server,
	)
	ctx, cancel := context.WithCancel(context.Background())
	stream := client.PingStream(ctx)
	require.NoError(t, stream.Send(nil))
	require.NoError(t, stream.CloseRequest())
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
	assert.NoError(t, stream.CloseResponse())
}

func TestStreamingHandlerTracing(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	serverInterceptor, err := NewInterceptor(WithTracerProvider(traceProvider))
	require.NoError(t, err)
	pingClient, host, port := startServer([]connect.HandlerOption{
		connect.WithInterceptors(serverInterceptor, assertSpanInterceptor{t: t}),
	}, nil, okayPingServer())
	stream := pingClient.PingStream(context.Background())

	msg := &pingv1.PingStreamRequest{
		Data: []byte("Hello, otel!"),
	}
	size := proto.Size(msg)
	require.NoError(t, stream.Send(msg))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
	require.Len(t, spanRecorder.Ended(), 1)
	require.Equal(t, codes.Unset, spanRecorder.Ended()[0].Status().Code)
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + PingStreamMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(size),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(size),
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
	require.NoError(t, stream.Send(msg))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
	require.Len(t, spanRecorder.Ended(), 1)
	require.Equal(t, codes.Unset, spanRecorder.Ended()[0].Status().Code)
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + PingStreamMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(size),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(size),
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
		WithAttributeFilter(func(_ connect.Spec, value attribute.KeyValue) bool {
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
	require.NoError(t, stream.Send(msg))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
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
	require.NoError(t, stream.Send(msg))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + PingStreamMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(size),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageIDKey.Int(1),
						semconv.MessageUncompressedSizeKey.Int(size),
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
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{
		Data: []byte("Hello, otel!"),
	}))
	_, err = stream.Receive()
	require.Error(t, err)
	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
	assert.Len(t, handlerSpanRecorder.Ended(), 1)
	assert.Len(t, clientSpanRecorder.Ended(), 1)
	assert.Equal(t, codes.Error, handlerSpanRecorder.Ended()[0].Status().Code)
	assert.Equal(t, codes.Error, clientSpanRecorder.Ended()[0].Status().Code)
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
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{
		Data: []byte("Hello, otel!"),
	}))
	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
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
	require.NoError(t, err)
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

func TestServerSpanStatus(t *testing.T) {
	t.Parallel()
	var propagator propagation.TraceContext
	for _, testcase := range serverSpanStatusTestCases() {
		spanRecorder := tracetest.NewSpanRecorder()
		traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
		clientSpanRecorder := tracetest.NewSpanRecorder()
		clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
		serverInterceptor, err := NewInterceptor(
			WithTracerProvider(traceProvider),
			WithoutTraceEvents(),
		)
		require.NoError(t, err)
		clientInterceptor, err := NewInterceptor(
			WithPropagator(propagator),
			WithTracerProvider(clientTraceProvider),
		)
		require.NoError(t, err)
		pingClient, _, _ := startServer([]connect.HandlerOption{
			connect.WithInterceptors(serverInterceptor),
		}, []connect.ClientOption{
			connect.WithInterceptors(clientInterceptor),
		}, &pluggablePingServer{
			ping: func(_ context.Context, _ *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
				return nil, connect.NewError(testcase.connectCode, errors.New(testcase.connectCode.String()))
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
		serverInterceptor, err := NewInterceptor(
			WithTracerProvider(handlerTraceProvider),
			WithoutTraceEvents(),
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
			}, &pluggablePingServer{
				pingStream: func(_ context.Context, _ *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse]) error {
					return connect.NewError(testcase.connectCode, errors.New(testcase.connectCode.String()))
				},
			})
		stream := client.PingStream(context.Background())
		require.NoError(t, stream.Send(&pingv1.PingStreamRequest{
			Data: []byte("Hello, otel!"),
		}))
		_, err = stream.Receive()
		require.Error(t, err)
		require.NoError(t, stream.CloseRequest())
		require.NoError(t, stream.CloseResponse())
		assert.Len(t, handlerSpanRecorder.Ended(), 1)
		assert.Len(t, clientSpanRecorder.Ended(), 1)
		assert.Equal(t, testcase.wantServerSpanCode, handlerSpanRecorder.Ended()[0].Status().Code)
		assert.Equal(t, testcase.wantServerSpanDescription, handlerSpanRecorder.Ended()[0].Status().Description)
		assert.Equal(t, codes.Error, clientSpanRecorder.Ended()[0].Status().Code)
	}
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
	require.Len(t, got, len(want), "unexpected spans length")
	for i, span := range got {
		wantEvents := want[i].events
		wantAttributes := want[i].attrs
		assert.False(t, span.StartTime().IsZero(), "span start time is nil")
		assert.Equal(t, want[i].spanName, span.Name(), "unexpected span name")
		gotEvents := span.Events()
		require.Len(t, gotEvents, len(wantEvents), "unexpected events length")
		for i, e := range wantEvents {
			if e.Name != gotEvents[i].Name {
				t.Error("names do not match")
			}
			diff := cmp.Diff(e.Attributes, gotEvents[i].Attributes,
				cmp.Comparer(func(x, y attribute.KeyValue) bool {
					return x.Value == y.Value && x.Key == y.Key
				}))
			assert.Empty(t, diff)
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

type assertSpanInterceptor struct{ t testing.TB }

func (i assertSpanInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		i.assertSpanContext(ctx)
		return next(ctx, request)
	}
}
func (i assertSpanInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		i.assertSpanContext(ctx)
		return next(ctx, spec)
	}
}
func (i assertSpanInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		i.assertSpanContext(ctx)
		return next(ctx, conn)
	}
}
func (i assertSpanInterceptor) assertSpanContext(ctx context.Context) {
	if !traceapi.SpanContextFromContext(ctx).IsValid() {
		i.t.Error("invalid span context")
	}
}

func TestWithTraceParentResponseHeader(t *testing.T) {
	t.Parallel()

	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))

	serverInterceptor, err := NewInterceptor(
		WithTraceParentResponseHeader(),
		WithTracerProvider(traceProvider),
		WithPropagator(propagation.TraceContext{}),
	)
	require.NoError(t, err)

	client, _, _ := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(serverInterceptor),
		},
		[]connect.ClientOption{},
		&pluggablePingServer{
			ping: func(_ context.Context, _ *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
				return connect.NewResponse(&pingv1.PingResponse{}), nil
			},
		})

	pingRequest := connect.NewRequest(&pingv1.PingRequest{Id: 1})
	response, err := client.Ping(context.Background(), pingRequest)
	require.NoError(t, err)

	// Check that the traceparent header is present in the response
	traceparent := response.Header().Get("traceparent")
	assert.NotEmpty(t, traceparent, "traceparent header should be present in response")

	// Validate traceparent format
	assertValidTraceParent(t, traceparent)
}

func TestWithTraceParentResponseHeaderStreaming(t *testing.T) {
	t.Parallel()

	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))

	serverInterceptor, err := NewInterceptor(
		WithTraceParentResponseHeader(),
		WithTracerProvider(traceProvider),
		WithPropagator(propagation.TraceContext{}),
	)
	require.NoError(t, err)

	client, _, _ := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(serverInterceptor),
		},
		[]connect.ClientOption{},
		&pluggablePingServer{
			pingStream: func(_ context.Context, stream *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse]) error {
				_, _ = stream.Receive()
				return stream.Send(&pingv1.PingStreamResponse{})
			},
		})

	stream := client.PingStream(context.Background())
	require.NoError(t, stream.Send(&pingv1.PingStreamRequest{}))
	require.NoError(t, stream.CloseRequest())

	_, err = stream.Receive()
	require.NoError(t, err)
	require.NoError(t, stream.CloseResponse())

	// Check that the traceparent header is present in the response headers
	traceparent := stream.ResponseHeader().Get("traceparent")
	assert.NotEmpty(t, traceparent, "traceparent header should be present in streaming response")

	// Validate traceparent format
	assertValidTraceParent(t, traceparent)
}

// assertValidTraceParent validates that a traceparent header follows the W3C Trace Context format:
// "00-{32 hex chars}-{16 hex chars}-{2 hex chars}"
func assertValidTraceParent(t *testing.T, traceparent string) {
	t.Helper()

	parts := strings.Split(traceparent, "-")
	assert.Len(t, parts, 4, "traceparent should have 4 parts separated by dashes")
	if len(parts) == 4 {
		assert.Equal(t, "00", parts[0], "version should be 00")
		assert.Len(t, parts[1], 32, "trace-id should be 32 hex characters")
		assert.Len(t, parts[2], 16, "span-id should be 16 hex characters")
		assert.Len(t, parts[3], 2, "trace-flags should be 2 hex characters")

		// Validate that trace-id and span-id contain only hex characters
		assert.Regexp(t, "^[0-9a-f]{32}$", parts[1], "trace-id should contain only lowercase hex characters")
		assert.Regexp(t, "^[0-9a-f]{16}$", parts[2], "span-id should contain only lowercase hex characters")
		assert.Regexp(t, "^[0-9a-f]{2}$", parts[3], "trace-flags should contain only lowercase hex characters")
	}
}
