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

	connectProtocol = "connect"
	metricKeyFormat = "rpc.%s.%s"
	duration        = "duration"
	requestSize     = "request.size"
	responseSize    = "response.size"
	requestsPerRPC  = "requests_per_rpc"
	responsesPerRPC = "responses_per_rpc"

	/* non otel specified metrics */
	firstWriteDelay      = "first_write_delay"
	interReceiveDuration = "inter_receive_duration"
	interSendDuration    = "inter_send_duration"
)

func formatkeys(metricType InterceptorType, metricName string) string {
	return fmt.Sprintf(metricKeyFormat, metricType, metricName)
}

type metricsConfig struct {
	Filter          func(context.Context, *Request) bool
	Provider        metric.MeterProvider
	propagators     propagation.TextMapPropagator
	Meter           metric.Meter
	interceptorType InterceptorType
}

type metricsInterceptor struct {
	config               metricsConfig
	duration             syncint64.Histogram
	requestSize          syncint64.Histogram
	responseSize         syncint64.Histogram
	requestsPerRPC       syncint64.Histogram
	responsesPerRPC      syncint64.Histogram
	firstWriteDelay      syncint64.Histogram
	interReceiveDuration syncint64.Histogram
	interSendDuration    syncint64.Histogram
}

func newMetricsInterceptor(metricConfig metricsConfig) (*metricsInterceptor, error) {
	m := metricsInterceptor{
		config: metricConfig,
	}
	var err error
	intProvider := m.config.Meter.SyncInt64()
	m.duration, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, duration), instrument.WithUnit(unit.Milliseconds))
	if err != nil {
		return nil, err
	}
	m.requestSize, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, requestSize), instrument.WithUnit(unit.Bytes))
	if err != nil {
		return nil, err
	}
	m.responseSize, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, responseSize), instrument.WithUnit(unit.Bytes))
	if err != nil {
		return nil, err
	}
	m.requestsPerRPC, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, requestsPerRPC), instrument.WithUnit(unit.Dimensionless))
	if err != nil {
		return nil, err
	}
	m.responsesPerRPC, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, responsesPerRPC), instrument.WithUnit(unit.Dimensionless))
	if err != nil {
		return nil, err
	}
	m.firstWriteDelay, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, firstWriteDelay), instrument.WithUnit(unit.Milliseconds))
	if err != nil {
		return nil, err
	}
	m.interReceiveDuration, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, interReceiveDuration), instrument.WithUnit(unit.Milliseconds))
	if err != nil {
		return nil, err
	}
	m.interSendDuration, err = intProvider.Histogram(formatkeys(metricConfig.interceptorType, interSendDuration), instrument.WithUnit(unit.Milliseconds))
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (i *metricsInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		requestStartTime := time.Now()
		r := &Request{
			Spec:   request.Spec(),
			Peer:   request.Peer(),
			Header: request.Header(),
		}
		if i.config.Filter != nil {
			if !i.config.Filter(ctx, r) {
				return next(ctx, request)
			}
		}
		response, err := next(ctx, request)
		attrs := attributesFromRequest(r)
		protocol := parseProtocol(request.Header())
		attrs = append(attrs, statusCodeAttribute(protocol, err))
		if err != nil {
			return nil, err
		}
		if err == nil {
			if msg, ok := response.Any().(proto.Message); ok {
				size := proto.Size(msg)
				i.responseSize.Record(ctx, int64(size), attrs...)
			}
		}
		if msg, ok := request.Any().(proto.Message); ok {
			size := proto.Size(msg)
			i.requestSize.Record(ctx, int64(size), attrs...)
		}
		i.duration.Record(ctx, time.Since(requestStartTime).Milliseconds(), attrs...)
		return response, err
	}
}

func (i *metricsInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		requestStartTime := time.Now()
		var lastReceive, lastSend time.Time
		conn := next(ctx, spec)
		r := &Request{
			Spec:   conn.Spec(),
			Peer:   conn.Peer(),
			Header: conn.RequestHeader(),
		}
		if i.config.Filter != nil {
			if !i.config.Filter(ctx, r) {
				return next(ctx, spec)
			}
		}
		attrs := attributesFromRequest(r)

		ret := &streamingClientInterceptor{
			StreamingClientConn: conn,
			payloadInterceptor: payloadInterceptor[connect.StreamingClientConn]{
				conn: conn,
				receive: func(msg any, p *payloadInterceptor[connect.StreamingClientConn]) error {
					i.requestsPerRPC.Record(ctx, 1, attrs...)
					i.responsesPerRPC.Record(ctx, 1, attrs...)
					err := p.conn.Receive(msg)
					if err != nil {
						attrs = append(attrs, statusCodeAttribute(parseProtocol(conn.RequestHeader()), err))
						return err
					}
					if msg, ok := msg.(proto.Message); ok {
						size := proto.Size(msg)
						i.requestSize.Record(ctx, int64(size), attrs...)
					}
					if !lastReceive.Equal(time.Time{}) {
						i.interReceiveDuration.Record(ctx, int64(time.Since(lastReceive)), attrs...)
					}
					lastReceive = time.Now()
					return nil
				},
				send: func(msg any, p *payloadInterceptor[connect.StreamingClientConn]) error {
					err := p.conn.Send(msg)
					if !requestStartTime.Equal(time.Time{}) {
						i.firstWriteDelay.Record(ctx, time.Since(requestStartTime).Milliseconds(), attrs...)
						requestStartTime = time.Time{}
					}

					if err != nil {
						attrs = append(attrs, statusCodeAttribute(parseProtocol(conn.RequestHeader()), err))
						return err
					}
					if msg, ok := msg.(proto.Message); ok {
						size := proto.Size(msg)
						i.responseSize.Record(ctx, int64(size), attrs...)
					}
					if !lastSend.Equal(time.Time{}) {
						i.interReceiveDuration.Record(ctx, time.Since(lastSend).Milliseconds(), attrs...)
					}
					lastSend = time.Now()
					return nil
				},
			},
		}
		return ret
	}
}

func (i *metricsInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		requestStartTime := time.Now()
		r := &Request{
			Spec:   conn.Spec(),
			Peer:   conn.Peer(),
			Header: conn.RequestHeader(),
		}
		if i.config.Filter != nil {
			if !i.config.Filter(ctx, r) {
				return next(ctx, conn)
			}
		}
		var lastReceive, lastSend time.Time
		attrs := attributesFromRequest(r)
		ret := &streamingHandlerInterceptor{
			StreamingHandlerConn: conn,
			payloadInterceptor: payloadInterceptor[connect.StreamingHandlerConn]{
				conn: conn,
				receive: func(msg any, p *payloadInterceptor[connect.StreamingHandlerConn]) error {
					i.requestsPerRPC.Record(ctx, 1, attrs...)
					i.responsesPerRPC.Record(ctx, 1, attrs...)
					err := p.conn.Receive(msg)
					if err != nil {
						attrs = append(attrs, statusCodeAttribute(parseProtocol(conn.RequestHeader()), err))
						return err
					}
					if msg, ok := msg.(proto.Message); ok {
						size := proto.Size(msg)
						i.requestSize.Record(ctx, int64(size), attrs...)
					}
					if !lastReceive.Equal(time.Time{}) {
						i.interReceiveDuration.Record(ctx, int64(time.Since(lastReceive)), attrs...)
					}
					lastReceive = time.Now()
					return nil
				},
				send: func(msg any, p *payloadInterceptor[connect.StreamingHandlerConn]) error {
					err := p.conn.Send(msg)
					if !requestStartTime.Equal(time.Time{}) {
						i.firstWriteDelay.Record(ctx, time.Since(requestStartTime).Milliseconds(), attrs...)
						requestStartTime = time.Time{}
					}

					if err != nil {
						attrs = append(attrs, statusCodeAttribute(parseProtocol(conn.RequestHeader()), err))
						return err
					}
					if msg, ok := msg.(proto.Message); ok {
						size := proto.Size(msg)
						i.responseSize.Record(ctx, int64(size), attrs...)
					}
					if !lastSend.Equal(time.Time{}) {
						i.interReceiveDuration.Record(ctx, time.Since(lastSend).Milliseconds(), attrs...)
					}
					lastSend = time.Now()
					return nil
				},
			},
		}
		//ret.startTime <- requestStartTime
		//ret.lastSend <- requestStartTime
		//ret.lastReceive <- requestStartTime
		return next(ctx, ret)

	}
}

func attributesFromRequest(r *Request) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	host, port, err := net.SplitHostPort(r.Peer.Addr)
	if err == nil {
		attrs = append(attrs,
			semconv.NetPeerNameKey.String(host),
			semconv.NetPeerPortKey.String(port),
		)
	}
	if addr := r.Peer.Addr; addr != "" {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			attrs = append(attrs, semconv.NetPeerNameKey.String(addr))
		} else {
			attrs = append(
				attrs,
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
			)
		}
	}
	name := strings.TrimLeft(r.Spec.Procedure, "/")
	protocol := parseProtocol(r.Header)
	attrs = append(attrs, semconv.RPCSystemKey.String(protocol))
	attrs = append(attrs, parseProcedure(name)...)
	return attrs
}
