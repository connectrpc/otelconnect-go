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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bufbuild/connect-go"
	pingv1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1"
	"github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1/pingv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric/unit"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	traceapi "go.opentelemetry.io/otel/trace"
)

const (
	messagesPerRequest       = 2
	successString            = "success"
	bufConnect               = "buf_connect"
	CumSumMethod             = "CumSum"
	PingMethod               = "Ping"
	FailMethod               = "Fail"
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
	rpcBufConnectStatusCode  = "rpc.buf_connect.status_code"
)

func TestStreamingMetrics(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	var now time.Time
	connectClient, host, port := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(New(
				WithMeterProvider(meterProvider), optionFunc(func(c *config) {
					c.now = func() time.Time {
						now = now.Add(time.Second)
						return now
					}
				}))),
		}, []connect.ClientOption{}, happyPingServer())
	stream := connectClient.CumSum(context.Background())
	err := stream.Send(&pingv1.CumSumRequest{Number: 12})
	if err != nil {
		t.Error(err)
	}
	_, err = stream.Receive()
	if err != nil {
		t.Error(err)
	}
	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
	metrics, err := metricReader.Collect(context.Background())
	if err != nil {
		t.Error(err)
	}
	diff := cmp.Diff(metricdata.ResourceMetrics{
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
						Unit: unit.Milliseconds,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key(rpcBufConnectStatusCode).String(successString),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 1,
									Sum:   1000.0,
									Min:   ptr(1000.0),
									Max:   ptr(1000.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcServerRequestSize,
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 1,
									Sum:   2.0,
									Min:   ptr(2.0),
									Max:   ptr(2.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcServerResponseSize,
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 2,
									Sum:   4.0,
									Min:   ptr(2.0),
									Max:   ptr(2.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcServerRequestsPerRPC,
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
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
						Name: rpcServerResponsesPerRPC,
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 2,
									Sum:   2,
									Min:   ptr(1.0),
									Max:   ptr(1.0),
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
	connectClient, host, port := startServer(
		[]connect.HandlerOption{},
		[]connect.ClientOption{
			connect.WithInterceptors(New(
				WithMeterProvider(meterProvider), optionFunc(func(c *config) {
					c.now = func() time.Time {
						now = now.Add(time.Second)
						return now
					}
				}))),
		}, happyPingServer())
	stream := connectClient.CumSum(context.Background())
	err := stream.Send(&pingv1.CumSumRequest{Number: 12})
	if err != nil {
		t.Error(err)
	}
	require.NoError(t, stream.CloseRequest())
	_, err = stream.Receive()
	if err != nil {
		t.Error(err)
	}
	require.NoError(t, stream.CloseResponse())
	metrics, err := metricReader.Collect(context.Background())
	if err != nil {
		t.Error(err)
	}

	diff := cmp.Diff(metricdata.ResourceMetrics{
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
						Unit: unit.Milliseconds,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
										attribute.Key(rpcBufConnectStatusCode).String(successString),
									),
									Count: 1,
									Sum:   1000.0,
									Min:   ptr(1000.0),
									Max:   ptr(1000.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientRequestSize,
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 1,
									Sum:   2.0,
									Min:   ptr(2.0),
									Max:   ptr(2.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientResponseSize,
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 1,
									Sum:   2.0,
									Min:   ptr(2.0),
									Max:   ptr(2.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientRequestsPerRPC,
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
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
						Name: rpcClientResponsesPerRPC,
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 1,
									Sum:   1,
									Min:   ptr(1.0),
									Max:   ptr(1.0),
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
	connectClient, host, port := startServer(
		[]connect.HandlerOption{},
		[]connect.ClientOption{
			connect.WithInterceptors(New(
				WithMeterProvider(meterProvider), optionFunc(func(c *config) {
					c.now = func() time.Time {
						now = now.Add(time.Second)
						return now
					}
				}))),
		}, failPingServer())
	stream := connectClient.CumSum(context.Background())
	err := stream.Send(&pingv1.CumSumRequest{Number: 12})
	if err != nil {
		t.Error(err)
	}
	require.NoError(t, stream.CloseRequest())
	_, err = stream.Receive()
	require.NoError(t, err)
	_, err = stream.Receive()
	require.NoError(t, err)
	_, err = stream.Receive()
	require.Error(t, err)
	require.NoError(t, stream.CloseResponse())
	metrics, err := metricReader.Collect(context.Background())
	if err != nil {
		t.Error(err)
	}
	diff := cmp.Diff(metricdata.ResourceMetrics{
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
						Unit: "ms",
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
										attribute.Key(rpcBufConnectStatusCode).String("data_loss"),
									),
									Count: 1,
									Sum:   1000.0,
									Min:   ptr(1000.0),
									Max:   ptr(1000.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientRequestSize,
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 1,
									Sum:   2.0,
									Min:   ptr(2.0),
									Max:   ptr(2.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientResponseSize,
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 2,
									Sum:   4.0,
									Min:   ptr(2.0),
									Max:   ptr(2.0),
								},
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key(rpcBufConnectStatusCode).String("data_loss"),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 1,
									Sum:   0.0,
									Min:   ptr(0.0),
									Max:   ptr(0.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcClientRequestsPerRPC,
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
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
						Name: rpcClientResponsesPerRPC,
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 2,
									Sum:   2,
									Min:   ptr(1.0),
									Max:   ptr(1.0),
								},
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
										attribute.Key(rpcBufConnectStatusCode).String("data_loss"),
									),
									Count: 1,
									Sum:   1,
									Min:   ptr(1.0),
									Max:   ptr(1.0),
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
	connectClient, host, port := startServer(
		[]connect.HandlerOption{
			connect.WithInterceptors(New(
				WithMeterProvider(meterProvider), optionFunc(func(c *config) {
					c.now = func() time.Time {
						now = now.Add(time.Second)
						return now
					}
				}))),
		}, []connect.ClientOption{}, failPingServer())
	stream := connectClient.CumSum(context.Background())
	err := stream.Send(&pingv1.CumSumRequest{Number: 12})
	if err != nil {
		t.Error(err)
	}
	require.NoError(t, stream.CloseRequest())
	_, err = stream.Receive()
	require.NoError(t, err)
	_, err = stream.Receive()
	require.NoError(t, err)
	_, err = stream.Receive()
	require.Error(t, err)
	require.NoError(t, stream.CloseResponse())
	metrics, err := metricReader.Collect(context.Background())
	if err != nil {
		t.Error(err)
	}
	diff := cmp.Diff(metricdata.ResourceMetrics{
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
						Unit: unit.Milliseconds,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key(rpcBufConnectStatusCode).String("data_loss"),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 1,
									Sum:   1000.0,
									Min:   ptr(1000.0),
									Max:   ptr(1000.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcServerRequestSize,
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 1,
									Sum:   2.0,
									Min:   ptr(2.0),
									Max:   ptr(2.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcServerResponseSize,
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 2,
									Sum:   4.0,
									Min:   ptr(2.0),
									Max:   ptr(2.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: rpcServerRequestsPerRPC,
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
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
						Name: rpcServerResponsesPerRPC,
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String(bufConnect),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCMethodKey.String(CumSumMethod),
									),
									Count: 2,
									Sum:   2,
									Min:   ptr(1.0),
									Max:   ptr(1.0),
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
	interceptor := New(WithMeterProvider(meterProvider), optionFunc(func(c *config) {
		c.now = func() time.Time {
			now = now.Add(time.Second)
			return now
		}
	}))
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(interceptor),
	}, happyPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 12)); err != nil {
		t.Errorf(err.Error())
	}
	metrics, err := metricReader.Collect(context.Background())
	if err != nil {
		t.Error(err)
	}
	diff := cmp.Diff(metricdata.ResourceMetrics{
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
						Unit: unit.Milliseconds,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key(rpcBufConnectStatusCode).String(successString),
										semconv.RPCMethodKey.String(PingMethod),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCSystemKey.String(bufConnect),
									),
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
						Name: rpcClientRequestSize,
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key(rpcBufConnectStatusCode).String(successString),
										semconv.RPCMethodKey.String(PingMethod),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCSystemKey.String(bufConnect),
									),
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
						Name: rpcClientResponseSize,
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key(rpcBufConnectStatusCode).String(successString),
										semconv.RPCMethodKey.String(PingMethod),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCSystemKey.String(bufConnect),
									),
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
						Name: rpcClientRequestsPerRPC,
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key(rpcBufConnectStatusCode).String(successString),
										semconv.RPCMethodKey.String(PingMethod),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCSystemKey.String(bufConnect),
									),
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
						Name: rpcClientResponsesPerRPC,
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerNameKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key(rpcBufConnectStatusCode).String(successString),
										semconv.RPCMethodKey.String(PingMethod),
										semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
										semconv.RPCSystemKey.String(bufConnect),
									),
									Count: 1,
									Sum:   1,
									Min:   ptr(1.0),
									Max:   ptr(1.0),
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
	interceptor := New(WithMeterProvider(meterProvider), WithoutMetrics())
	pingClient, _, _ := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(interceptor),
	}, happyPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 12)); err != nil {
		t.Errorf(err.Error())
	}
	metrics, err := metricReader.Collect(context.Background())
	if err != nil {
		t.Error(err)
	}
	if len(metrics.ScopeMetrics) != 0 {
		t.Error("metrics unexpectedly recorded")
	}
}

func TestWithoutTracing(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	pingClient, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(New(WithTracerProvider(traceProvider), WithoutTracing())),
	}, nil, happyPingServer())
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
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(New(WithTracerProvider(clientTraceProvider))),
	}, happyPingServer())
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
				attribute.Key(rpcBufConnectStatusCode).String(successString),
			},
		},
	}, clientSpanRecorder.Ended())
}

func TestHandlerFailCall(t *testing.T) {
	t.Parallel()
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(New(WithTracerProvider(clientTraceProvider))),
	}, happyPingServer())
	_, err := pingClient.Fail(
		context.Background(),
		connect.NewRequest(&pingv1.FailRequest{Code: int32(connect.CodeInternal)}),
	)
	if err == nil {
		t.Fatal("expecting error, got nil")
	}
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
	pingClient, host, port := startServer([]connect.HandlerOption{
		connect.WithInterceptors(New(WithTracerProvider(serverTraceProvider), WithFilter(func(ctx context.Context, request *Request) bool {
			return false
		}))),
	}, []connect.ClientOption{
		connect.WithInterceptors(New(WithTracerProvider(clientTraceProvider))),
	}, happyPingServer())
	if _, err := pingClient.Ping(context.Background(), requestOfSize(1, 0)); err != nil {
		t.Errorf(err.Error())
	}
	assertSpans(t, []wantSpans{}, serverSpanRecorder.Ended())
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
				attribute.Key(rpcBufConnectStatusCode).String(successString),
			},
		},
	}, clientSpanRecorder.Ended())
}

func TestBasicFilter(t *testing.T) {
	t.Parallel()
	headerKey, headerVal := "Some-Header", "foobar"
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	pingClient, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(New(WithTracerProvider(traceProvider), WithFilter(func(ctx context.Context, request *Request) bool {
			return false
		}))),
	}, nil, happyPingServer())
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
	pingClient, host, port := startServer([]connect.HandlerOption{
		connect.WithInterceptors(New(WithTracerProvider(traceProvider), WithFilter(func(ctx context.Context, request *Request) bool {
			return request.Header.Get(headerKey) == headerVal
		}))),
	}, nil, happyPingServer())
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
				attribute.Key(rpcBufConnectStatusCode).String(successString),
			},
		},
	}, spanRecorder.Ended())
}

func TestInterceptors(t *testing.T) {
	t.Parallel()
	const largeMessageSize = 1000
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	pingClient, host, port := startServer([]connect.HandlerOption{
		connect.WithInterceptors(New(WithTracerProvider(traceProvider))),
	}, nil, happyPingServer())
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
				attribute.Key(rpcBufConnectStatusCode).String(successString),
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
				attribute.Key(rpcBufConnectStatusCode).String(successString),
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
	client, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(New(
			WithPropagator(propagation.TraceContext{}),
			WithTracerProvider(trace.NewTracerProvider()),
		)),
	}, nil, &pluggablePingServer{ping: assertTraceParent})
	resp, err := client.Ping(context.Background(), connect.NewRequest(&pingv1.PingRequest{Id: 1}))
	assert.NoError(t, err)
	assert.Equal(t, int64(1), resp.Msg.Id)
}

func TestStreamingHandlerNoTraceParent(t *testing.T) {
	t.Parallel()
	assertTraceParent := func(ctx context.Context, stream *connect.BidiStream[pingv1.CumSumRequest, pingv1.CumSumResponse]) error {
		assert.Zero(t, stream.RequestHeader().Get(TraceParentKey))
		assert.NoError(t, stream.Send(&pingv1.CumSumResponse{Sum: 1}))
		return nil
	}
	client, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(New(
			WithPropagator(propagation.TraceContext{}),
			WithTracerProvider(trace.NewTracerProvider()),
		)),
	}, nil, &pluggablePingServer{cumSum: assertTraceParent},
	)
	stream := client.CumSum(context.Background())
	assert.NoError(t, stream.CloseRequest())
	resp, err := stream.Receive()
	assert.NoError(t, stream.CloseResponse())
	assert.NoError(t, err)
	assert.Equal(t, int64(1), resp.Sum)
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
	client, _, _ := startServer(
		[]connect.HandlerOption{connect.WithInterceptors(New(
			WithPropagator(propagator),
			WithTracerProvider(handlerTraceProvider),
			WithTrustRemote(),
		))}, []connect.ClientOption{
			connect.WithInterceptors(New(
				WithPropagator(propagator),
				WithTracerProvider(clientTraceProvider),
			)),
		}, happyPingServer())
	_, err := client.Ping(ctx, connect.NewRequest(&pingv1.PingRequest{Id: 1}))
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
	client, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(connect.UnaryInterceptorFunc(func(unaryFunc connect.UnaryFunc) connect.UnaryFunc {
			return func(_ context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
				ctx, span = trace.NewTracerProvider().Tracer("test").Start(context.Background(), "test")
				return unaryFunc(ctx, request)
			}
		})),
		connect.WithInterceptors(New(
			WithPropagator(propagation.TraceContext{}),
			WithTracerProvider(traceProvider),
			WithTrustRemote(),
		)),
	}, nil, happyPingServer())
	resp, err := client.Ping(context.Background(), connect.NewRequest(&pingv1.PingRequest{Id: 1}))
	assert.NoError(t, err)
	assert.Equal(t, int64(1), resp.Msg.Id)
	assert.Equal(t, len(spanRecorder.Ended()), 1)
	recordedSpan := spanRecorder.Ended()[0]
	assert.True(t, recordedSpan.Parent().IsValid())
	assert.True(t, recordedSpan.Parent().Equal(span.SpanContext()))
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
	client, _, _ := startServer(
		[]connect.HandlerOption{connect.WithInterceptors(New(
			WithPropagator(propagator),
			WithTracerProvider(handlerTraceProvider),
		))}, []connect.ClientOption{
			connect.WithInterceptors(New(
				WithPropagator(propagator),
				WithTracerProvider(clientTraceProvider),
			)),
		}, happyPingServer())
	_, err := client.Ping(ctx, connect.NewRequest(&pingv1.PingRequest{Id: 1}))
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
	client, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(streamingHandlerInterceptorFunc(func(handlerFunc connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
			return func(_ context.Context, conn connect.StreamingHandlerConn) error {
				ctx, span = trace.NewTracerProvider().Tracer("test").Start(ctx, "test")
				return handlerFunc(ctx, conn)
			}
		})),
		connect.WithInterceptors(New(
			WithPropagator(propagation.TraceContext{}),
			WithTracerProvider(traceProvider),
		)),
	}, nil, happyPingServer(),
	)
	stream := client.CumSum(context.Background())
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
	client, _, _ := startServer(
		[]connect.HandlerOption{connect.WithInterceptors(New(
			WithPropagator(propagator),
			WithTracerProvider(handlerTraceProvider),
			WithTrustRemote(),
		))}, []connect.ClientOption{
			connect.WithInterceptors(New(
				WithPropagator(propagator),
				WithTracerProvider(clientTraceProvider),
			)),
		}, happyPingServer())
	stream := client.CumSum(ctx)
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
	client, _, _ := startServer(
		[]connect.HandlerOption{connect.WithInterceptors(New(
			WithPropagator(propagator),
			WithTracerProvider(handlerTraceProvider),
		))}, []connect.ClientOption{
			connect.WithInterceptors(New(
				WithPropagator(propagator),
				WithTracerProvider(clientTraceProvider),
			)),
		}, happyPingServer())
	stream := client.CumSum(ctx)
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	assert.Equal(t, len(handlerSpanRecorder.Ended()), 1)
	assert.Equal(t, len(clientSpanRecorder.Ended()), 1)
	assertSpanLink(t, rootSpan, clientSpanRecorder.Ended()[0], handlerSpanRecorder.Ended()[0])
}

func TestStreamingClientPropagation(t *testing.T) {
	t.Parallel()
	assertTraceParent := func(ctx context.Context, stream *connect.BidiStream[pingv1.CumSumRequest, pingv1.CumSumResponse]) error {
		assert.NotZero(t, stream.RequestHeader().Get(TraceParentKey))
		assert.NoError(t, stream.Send(&pingv1.CumSumResponse{Sum: 1}))
		return nil
	}
	client, _, _ := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(New(
			WithPropagator(propagation.TraceContext{}),
			WithTracerProvider(trace.NewTracerProvider()),
		)),
	}, &pluggablePingServer{cumSum: assertTraceParent},
	)
	stream := client.CumSum(context.Background())
	assert.NoError(t, stream.CloseRequest())
	resp, err := stream.Receive()
	assert.NoError(t, stream.CloseResponse())
	assert.NoError(t, err)
	assert.Equal(t, int64(1), resp.Sum)
}

func TestStreamingHandlerTracing(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	pingClient, host, port := startServer([]connect.HandlerOption{
		connect.WithInterceptors(New(WithTracerProvider(traceProvider))),
	}, nil, happyPingServer())
	stream := pingClient.CumSum(context.Background())

	assert.NoError(t, stream.Send(&pingv1.CumSumRequest{Number: 1}))
	_, err := stream.Receive()
	assert.NoError(t, err)
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + CumSumMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageUncompressedSizeKey.Int(2),
						semconv.MessageIDKey.Int(1),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageUncompressedSizeKey.Int(2),
						semconv.MessageIDKey.Int(1),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageUncompressedSizeKey.Int(2),
						semconv.MessageIDKey.Int(2),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(CumSumMethod),
				attribute.Key(rpcBufConnectStatusCode).String(successString),
			},
		},
	}, spanRecorder.Ended())
}

func TestStreamingClientTracing(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(New(WithTracerProvider(traceProvider))),
	}, happyPingServer())
	stream := pingClient.CumSum(context.Background())

	assert.NoError(t, stream.Send(&pingv1.CumSumRequest{Number: 1}))
	_, err := stream.Receive()
	assert.NoError(t, err)
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + CumSumMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageUncompressedSizeKey.Int(2),
						semconv.MessageIDKey.Int(1),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageUncompressedSizeKey.Int(2),
						semconv.MessageIDKey.Int(1),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(CumSumMethod),
				attribute.Key(rpcBufConnectStatusCode).String(successString),
			},
		},
	}, spanRecorder.Ended())
}

func TestWithAttributeFilter(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(New(
			WithTracerProvider(traceProvider),
			WithAttributeFilter(func(request *Request, value attribute.KeyValue) bool {
				if value.Key == semconv.MessageIDKey {
					return false
				}
				if value.Key == semconv.RPCServiceKey {
					return false
				}
				return true
			},
			),
		)),
	}, happyPingServer())
	stream := pingClient.CumSum(context.Background())

	assert.NoError(t, stream.Send(&pingv1.CumSumRequest{Number: 1}))
	_, err := stream.Receive()
	assert.NoError(t, err)
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + CumSumMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageUncompressedSizeKey.Int(2),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCMethodKey.String(CumSumMethod),
				attribute.Key(rpcBufConnectStatusCode).String(successString),
			},
		},
	}, spanRecorder.Ended())
}

func TestWithoutServerPeerAttributes(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	pingClient, _, _ := startServer([]connect.HandlerOption{
		connect.WithInterceptors(New(
			WithTracerProvider(traceProvider),
			WithoutServerPeerAttributes(),
		)),
	}, nil, happyPingServer())
	stream := pingClient.CumSum(context.Background())
	assert.NoError(t, stream.Send(&pingv1.CumSumRequest{Number: 1}))
	_, err := stream.Receive()
	assert.NoError(t, err)
	assert.NoError(t, stream.CloseRequest())
	assert.NoError(t, stream.CloseResponse())
	assertSpans(t, []wantSpans{
		{
			spanName: pingv1connect.PingServiceName + "/" + CumSumMethod,
			events: []trace.Event{
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeReceived,
						semconv.MessageUncompressedSizeKey.Int(2),
						semconv.MessageIDKey.Int(1),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageUncompressedSizeKey.Int(2),
						semconv.MessageIDKey.Int(1),
					},
				},
				{
					Name: messageKey,
					Attributes: []attribute.KeyValue{
						semconv.MessageTypeSent,
						semconv.MessageUncompressedSizeKey.Int(2),
						semconv.MessageIDKey.Int(2),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.RPCSystemKey.String(bufConnect),
				semconv.RPCServiceKey.String(pingv1connect.PingServiceName),
				semconv.RPCMethodKey.String(CumSumMethod),
				attribute.Key(rpcBufConnectStatusCode).String(successString),
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

func ptr[T any](val T) *T {
	return &val
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
		cmpopts.SortSlices(func(x, y metricdata.HistogramDataPoint) bool {
			return x.Attributes.Len() > y.Attributes.Len()
		}),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint{}, "StartTime"),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint{}, "Time"),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint{}, "Bounds"),
		cmpopts.IgnoreFields(metricdata.HistogramDataPoint{}, "BucketCounts"),
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
