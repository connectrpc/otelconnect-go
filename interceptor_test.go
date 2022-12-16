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
)

const messagesPerRequest = 2

/*

| Attribute  | Type |
|---|---|---|---|---|
| rpc.system | string |
| rpc.service | string |
| rpc.method | string |
| net.peer.name | string |
| net.peer.port | int |

Bellow aren't added as per [specification](https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/trace/semantic_conventions/span-general.md#netsock-attributes)
"Higher-level instrumentations such as HTTP don't always have access to the
socket-level information and may not be able to populate socket-level attributes."

| net.transport | string |
| net.sock.family | string |
| net.sock.peer.addr | string |
| net.sock.peer.name | string |
| net.sock.peer.port | int |

*/

func TestStreamingMetrics(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	var now time.Time
	connectClient, host, port := startServer(
		[]connect.HandlerOption{
			WithTelemetry(
				WithMeterProvider(meterProvider), optionFunc(func(c *config) {
					c.now = func() time.Time {
						now = now.Add(time.Second)
						return now
					}
				})),
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
						Name: "rpc.server.duration",
						Unit: "ms",
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key("rpc.buf_connect.status_code").String("success"),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
						Name: "rpc.server.request.size",
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
									),
									Count: 2,
									Sum:   2.0,
									Min:   ptr(0.0),
									Max:   ptr(2.0),
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
					{
						Name: "rpc.server.response.size",
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
						Name: "rpc.server.requests_per_rpc",
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
					{
						Name: "rpc.server.responses_per_rpc",
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
			WithTelemetry(
				WithMeterProvider(meterProvider), optionFunc(func(c *config) {
					c.now = func() time.Time {
						now = now.Add(time.Second)
						return now
					}
				})),
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
						Name: "rpc.client.duration",
						Unit: "ms",
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
										attribute.Key("rpc.buf_connect.status_code").String("success"),
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
						Name: "rpc.client.request.size",
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
						Name: "rpc.client.response.size",
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
						Name: "rpc.client.requests_per_rpc",
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
						Name: "rpc.client.responses_per_rpc",
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
			WithTelemetry(
				WithMeterProvider(meterProvider), optionFunc(func(c *config) {
					c.now = func() time.Time {
						now = now.Add(time.Second)
						return now
					}
				})),
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
						Name: "rpc.client.duration",
						Unit: "ms",
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key("rpc.buf_connect.status_code").String("success"),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
						Name: "rpc.client.request.size",
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
									),
									Count: 2,
									Sum:   4.0,
									Min:   ptr(2.0),
									Max:   ptr(2.0),
								},
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key("rpc.buf_connect.status_code").String("data_loss"),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
						Name: "rpc.client.response.size",
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
						Name: "rpc.client.requests_per_rpc",
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
									),
									Count: 2,
									Sum:   2,
									Min:   ptr(1.0),
									Max:   ptr(1.0),
								},
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key("rpc.buf_connect.status_code").String("data_loss"),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
						Name: "rpc.client.responses_per_rpc",
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
									),
									Count: 2,
									Sum:   2,
									Min:   ptr(1.0),
									Max:   ptr(1.0),
								},
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key("rpc.buf_connect.status_code").String("data_loss"),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
			WithTelemetry(
				WithMeterProvider(meterProvider), optionFunc(func(c *config) {
					c.now = func() time.Time {
						now = now.Add(time.Second)
						return now
					}
				})),
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
						Name: "rpc.server.duration",
						Unit: "ms",
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key("rpc.buf_connect.status_code").String("data_loss"),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
						Name: "rpc.server.request.size",
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
						Name: "rpc.server.response.size",
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
						Name: "rpc.server.requests_per_rpc",
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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
						Name: "rpc.server.responses_per_rpc",
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										semconv.RPCSystemKey.String("buf_connect"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCMethodKey.String("CumSum"),
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

func TestMetrics(t *testing.T) {
	t.Parallel()
	metricReader, meterProvider := setupMetrics()
	var now time.Time
	interceptor := NewInterceptor(WithMeterProvider(meterProvider), optionFunc(func(c *config) {
		c.now = func() time.Time {
			now = now.Add(time.Second)
			return now
		}
	}))
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(interceptor),
	}, happyPingServer())
	if _, err := pingClient.Ping(context.Background(), RequestOfSize(1, 12)); err != nil {
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
						Name: "rpc.client.duration",
						Unit: "ms",
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key("rpc.buf_connect.status_code").String("success"),
										semconv.RPCMethodKey.String("Ping"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCSystemKey.String("buf_connect"),
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
						Name: "rpc.client.request.size",
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key("rpc.buf_connect.status_code").String("success"),
										semconv.RPCMethodKey.String("Ping"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCSystemKey.String("buf_connect"),
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
						Name: "rpc.client.response.size",
						Unit: unit.Bytes,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key("rpc.buf_connect.status_code").String("success"),
										semconv.RPCMethodKey.String("Ping"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCSystemKey.String("buf_connect"),
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
						Name: "rpc.client.requests_per_rpc",
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key("rpc.buf_connect.status_code").String("success"),
										semconv.RPCMethodKey.String("Ping"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCSystemKey.String("buf_connect"),
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
						Name: "rpc.client.responses_per_rpc",
						Unit: unit.Dimensionless,
						Data: metricdata.Histogram{
							DataPoints: []metricdata.HistogramDataPoint{
								{
									Attributes: attribute.NewSet(
										semconv.NetPeerIPKey.String(host),
										semconv.NetPeerPortKey.Int(port),
										attribute.Key("rpc.buf_connect.status_code").String("success"),
										semconv.RPCMethodKey.String("Ping"),
										semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
										semconv.RPCSystemKey.String("buf_connect"),
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
	interceptor := NewInterceptor(WithMeterProvider(meterProvider), WithoutMetrics())
	pingClient, _, _ := startServer(nil, []connect.ClientOption{
		connect.WithInterceptors(interceptor),
	}, happyPingServer())
	if _, err := pingClient.Ping(context.Background(), RequestOfSize(1, 12)); err != nil {
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
		WithTelemetry(WithTracerProvider(traceProvider), WithoutTracing()),
	}, nil, happyPingServer())
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
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		WithTelemetry(WithTracerProvider(clientTraceProvider)),
	}, happyPingServer())
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
				semconv.NetPeerIPKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String("buf_connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
				attribute.Key("rpc.buf_connect.status_code").String("success"),
			},
		},
	}, clientSpanRecorder.Ended())
}

func TestHandlerFailCall(t *testing.T) {
	t.Parallel()
	clientSpanRecorder := tracetest.NewSpanRecorder()
	clientTraceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(clientSpanRecorder))
	pingClient, host, port := startServer(nil, []connect.ClientOption{
		WithTelemetry(WithTracerProvider(clientTraceProvider)),
	}, happyPingServer())
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
				semconv.NetPeerIPKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String("buf_connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Fail"),
				attribute.Key("rpc.buf_connect.status_code").String("unimplemented"),
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
		WithTelemetry(WithTracerProvider(serverTraceProvider), WithFilter(func(ctx context.Context, request *Request) bool {
			return false
		})),
	}, []connect.ClientOption{
		WithTelemetry(WithTracerProvider(clientTraceProvider)),
	}, happyPingServer())
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
				semconv.NetPeerIPKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String("buf_connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
				attribute.Key("rpc.buf_connect.status_code").String("success"),
			},
		},
	}, clientSpanRecorder.Ended())
}

func TestBasicFilter(t *testing.T) {
	t.Parallel()
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	pingClient, _, _ := startServer([]connect.HandlerOption{
		WithTelemetry(WithTracerProvider(traceProvider), WithFilter(func(ctx context.Context, request *Request) bool {
			return false
		})),
	}, nil, happyPingServer())
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
	pingClient, host, port := startServer([]connect.HandlerOption{
		WithTelemetry(WithTracerProvider(traceProvider), WithFilter(func(ctx context.Context, request *Request) bool {
			return request.Header.Get("Some-Header") == "foobar"
		})),
	}, nil, happyPingServer())
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
				semconv.NetPeerIPKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String("buf_connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
				attribute.Key("rpc.buf_connect.status_code").String("success"),
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
		WithTelemetry(WithTracerProvider(traceProvider)),
	}, nil, happyPingServer())
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
				semconv.NetPeerIPKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String("buf_connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
				attribute.Key("rpc.buf_connect.status_code").String("success"),
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
				semconv.NetPeerIPKey.String(host),
				semconv.NetPeerPortKey.Int(port),
				semconv.RPCSystemKey.String("buf_connect"),
				semconv.RPCServiceKey.String("observability.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
				attribute.Key("rpc.buf_connect.status_code").String("success"),
			},
		},
	}, spanRecorder.Ended())
}

func TestPropagation(t *testing.T) {
	t.Parallel()
	assertTraceParent := func(ctx context.Context, req *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
		assert.NotZero(t, req.Header().Get("traceparent"))
		return connect.NewResponse(&pingv1.PingResponse{Id: req.Msg.Id}), nil
	}
	client, _, _ := startServer([]connect.HandlerOption{
		WithTelemetry(
			WithPropagator(propagation.TraceContext{}),
			WithTracerProvider(trace.NewTracerProvider()),
		),
	}, nil, &pluggablePingServer{ping: assertTraceParent})
	resp, err := client.Ping(context.Background(), connect.NewRequest(&pingv1.PingRequest{Id: 1}))
	assert.NoError(t, err)
	assert.Equal(t, int64(1), resp.Msg.Id)
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
		attribute.String("telemetry.sdk.version", "1.11.1"),
	)
}
