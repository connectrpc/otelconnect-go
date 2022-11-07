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
	"fmt"
	"github.com/bufbuild/connect-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"google.golang.org/protobuf/proto"
	"net"
	"strings"
	"time"
)

type InterceptorType string

const (
	Client InterceptorType = "client"
	Server InterceptorType = "server"

	MetricKeyFormat = "rpc.%s.%s"
	Duration        = "duration"
	RequestSize     = "request.size"
	ResponseSize    = "response.size"
	RequestsPerRPC  = "requests_per_rpc"
	ResponsesPerRPC = "responses_per_rpc"

	/* non otel specified metrics */
	FirstWriteDelay      = "first_write_delay"
	InterReceiveDuration = "inter_receive_duration"
	InterSendDuration    = "inter_send_duration"
)

func formatkeys(metricType InterceptorType, metricName string) string {
	return fmt.Sprintf(MetricKeyFormat, metricType, metricName)
}

type metricsConfig struct {
	Filter          func(context.Context, *Request) bool
	Provider        metric.MeterProvider
	propagators     propagation.TextMapPropagator
	Meter           metric.Meter
	interceptorType InterceptorType
}

type metricsInterceptor struct {
	config metricsConfig

	Duration             syncint64.Histogram
	RequestSize          syncint64.Histogram
	ResponseSize         syncint64.Histogram
	RequestsPerRPC       syncint64.Histogram
	ResponsesPerRPC      syncint64.Histogram
	FirstWriteDelay      syncint64.Histogram
	InterReceiveDuration syncint64.Histogram
	InterSendDuration    syncint64.Histogram
}

func newmetricsInterceptor(metricConfig metricsConfig) (*metricsInterceptor, error) {
	m := metricsInterceptor{
		config: metricConfig,
	}
	var err error
	intProvider := m.config.Meter.SyncInt64()
	m.Duration, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, Duration), instrument.WithUnit(unit.Milliseconds))
	if err != nil {
		return nil, err
	}
	m.RequestSize, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, RequestSize), instrument.WithUnit(unit.Bytes))
	if err != nil {
		return nil, err
	}
	m.ResponseSize, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, ResponseSize), instrument.WithUnit(unit.Bytes))
	if err != nil {
		return nil, err
	}
	m.RequestsPerRPC, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, RequestsPerRPC), instrument.WithUnit(unit.Dimensionless))
	if err != nil {
		return nil, err
	}
	m.ResponsesPerRPC, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, ResponsesPerRPC), instrument.WithUnit(unit.Dimensionless))
	if err != nil {
		return nil, err
	}
	m.FirstWriteDelay, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, FirstWriteDelay), instrument.WithUnit(unit.Milliseconds))
	if err != nil {
		return nil, err
	}
	m.InterReceiveDuration, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, InterReceiveDuration), instrument.WithUnit(unit.Milliseconds))
	if err != nil {
		return nil, err
	}
	m.InterSendDuration, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, InterSendDuration), instrument.WithUnit(unit.Milliseconds))
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (i *metricsInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return connect.UnaryFunc(func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		requestStartTime := time.Now()
		if i.config.Filter != nil {
			r := &Request{
				Spec:   request.Spec(),
				Peer:   request.Peer(),
				Header: request.Header(),
			}
			if !i.config.Filter(ctx, r) {
				return next(ctx, request)
			}
		}
		serviceMethod := strings.Split(strings.TrimLeft(request.Spec().Procedure, "/"), "/")
		var serviceName, methodName string
		if len(serviceMethod) == 2 {
			serviceName, methodName = serviceMethod[0], serviceMethod[1]
		}
		attrs := []attribute.KeyValue{
			semconv.RPCSystemKey.String("connect"),
			semconv.RPCServiceKey.String(serviceName),
			semconv.RPCMethodKey.String(methodName),
		}
		host, port, err := net.SplitHostPort(request.Peer().Addr)
		if err == nil {
			attrs = append(attrs,
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
			)
		}
		response, err := next(ctx, request)
		if err != nil {
			return nil, err
		}
		if err == nil {
			if msg, ok := response.Any().(proto.Message); ok {
				size := proto.Size(msg)
				i.ResponseSize.Record(ctx, int64(size), attrs...)
			}
		}
		if msg, ok := request.Any().(proto.Message); ok {
			size := proto.Size(msg)
			i.RequestSize.Record(ctx, int64(size), attrs...)
		}
		i.Duration.Record(ctx, time.Since(requestStartTime).Milliseconds(), attrs...)
		return response, err
	})
}

func (i *metricsInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		requestStartTime := time.Now()
		if i.config.Filter != nil {
			r := &Request{
				Spec:   conn.Spec(),
				Peer:   conn.Peer(),
				Header: conn.RequestHeader(),
			}
			if !i.config.Filter(ctx, r) {
				return next(ctx, spec)
			}
		}
		serviceMethod := strings.Split(strings.TrimLeft(conn.Spec().Procedure, "/"), "/")
		var serviceName, methodName string
		if len(serviceMethod) == 2 {
			serviceName, methodName = serviceMethod[0], serviceMethod[1]
		}
		attrs := []attribute.KeyValue{
			semconv.RPCSystemKey.String("connect"),
			semconv.RPCServiceKey.String(serviceName),
			semconv.RPCMethodKey.String(methodName),
		}
		host, port, err := net.SplitHostPort(conn.Peer().Addr)
		if err == nil {
			attrs = append(attrs,
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
			)
		}

		i.Duration.Record(ctx, time.Since(requestStartTime).Milliseconds(), attrs...)

		return &payloadStreamInterceptorClient{
			StreamingClientConn: conn,
			startTime:           requestStartTime,
			RequestsPerRPC: func() {
				i.RequestsPerRPC.Record(ctx, 1, attrs...)
			},
			ResponsesPerRPC: func() {
				i.ResponsesPerRPC.Record(ctx, 1, attrs...)
			},
			ResponseSize: func(size int64) {
				i.ResponseSize.Record(ctx, size, attrs...)
			},
			RequestSize: func(size int64) {
				i.RequestSize.Record(ctx, size, attrs...)
			},

			FirstWriteDelay: func(t time.Duration) {
				i.FirstWriteDelay.Record(ctx, t.Milliseconds(), attrs...)
			},
			InterReceiveDuration: func(t time.Duration) {
				i.InterReceiveDuration.Record(ctx, t.Milliseconds(), attrs...)
			},
			InterSendDuration: func(t time.Duration) {
				i.InterSendDuration.Record(ctx, t.Milliseconds(), attrs...)
			},
		}
	}
}

func (i *metricsInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		requestStartTime := time.Now()
		if i.config.Filter != nil {
			r := &Request{
				Spec:   conn.Spec(),
				Peer:   conn.Peer(),
				Header: conn.RequestHeader(),
			}
			if !i.config.Filter(ctx, r) {
				return next(ctx, conn)
			}
		}

		serviceMethod := strings.Split(strings.TrimLeft(conn.Spec().Procedure, "/"), "/")
		var serviceName, methodName string
		if len(serviceMethod) == 2 {
			serviceName, methodName = serviceMethod[0], serviceMethod[1]
		}

		attrs := []attribute.KeyValue{
			semconv.RPCSystemKey.String("connect"),
			semconv.RPCServiceKey.String(serviceName),
			semconv.RPCMethodKey.String(methodName),
		}
		host, port, err := net.SplitHostPort(conn.Peer().Addr)
		if err == nil {
			attrs = append(attrs,
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
			)
		}

		i.Duration.Record(ctx, time.Since(requestStartTime).Milliseconds(), attrs...)

		return next(ctx, &payloadStreamInterceptorHandler{
			StreamingHandlerConn: conn,
			startTime:            requestStartTime,
			RequestsPerRPC: func() {
				i.RequestsPerRPC.Record(ctx, 1, attrs...)
			},
			ResponsesPerRPC: func() {
				i.ResponsesPerRPC.Record(ctx, 1, attrs...)
			},
			ResponseSize: func(size int64) {
				i.ResponseSize.Record(ctx, int64(size), attrs...)
			},
			RequestSize: func(size int64) {
				i.RequestSize.Record(ctx, int64(size), attrs...)
			},

			FirstWriteDelay: func(t time.Duration) {
				i.FirstWriteDelay.Record(ctx, t.Milliseconds(), attrs...)
			},
			InterReceiveDuration: func(t time.Duration) {
				i.InterReceiveDuration.Record(ctx, t.Milliseconds(), attrs...)
			},
			InterSendDuration: func(t time.Duration) {
				i.InterSendDuration.Record(ctx, t.Milliseconds(), attrs...)
			},
		})
	}
}
