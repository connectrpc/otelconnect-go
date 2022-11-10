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
	"net"
	"strings"
	"sync"
	"time"

	"github.com/bufbuild/connect-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"google.golang.org/protobuf/proto"
)

type InterceptorType string

const (
	Client InterceptorType = "client"
	Server InterceptorType = "server"

	metricKeyFormat = "rpc.%s.%s"

	// Metrics as defined by https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/metrics/semantic_conventions/rpc-metrics.md
	duration        = "duration"
	requestSize     = "request.size"
	responseSize    = "response.size"
	requestsPerRPC  = "requests_per_rpc"
	responsesPerRPC = "responses_per_rpc"

	// Custom metrics
	// firstWriteDelay records the time from a stream being opened til the time of the first write
	// if the interceptor is a client interceptor this will be the time from the stream being
	// opened til the first message is opened.
	// If the interceptor is a server interceptor then the time will be the time from when the client
	// first connects til the time that the server replies with the first message.
	firstWriteDelay = "first_write_delay"
	// interReceiveDuration records the time between receiving consecutive messages.
	interReceiveDuration = "inter_receive_duration"
	// interSendDuration records the time between sending consecutive messages.
	interSendDuration = "inter_send_duration"
)

func formatkeys(metricType InterceptorType, metricName string) string {
	return fmt.Sprintf(metricKeyFormat, metricType, metricName)
}

type metricsConfig struct {
	Filter          func(context.Context, *Request) bool
	Provider        metric.MeterProvider
	Meter           metric.Meter
	interceptorType InterceptorType
}

type metricsInterceptor struct {
	config               metricsConfig
	now                  func() time.Time
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
	interceptor := metricsInterceptor{
		config: metricConfig,
		now:    time.Now,
	}
	var err error
	intProvider := interceptor.config.Meter.SyncInt64()
	interceptor.duration, err = intProvider.Histogram(
		formatkeys(metricConfig.interceptorType, duration),
		instrument.WithUnit(unit.Milliseconds),
	)
	if err != nil {
		return nil, err
	}
	interceptor.requestSize, err = intProvider.Histogram(
		formatkeys(metricConfig.interceptorType, requestSize),
		instrument.WithUnit(unit.Bytes),
	)
	if err != nil {
		return nil, err
	}
	interceptor.responseSize, err = intProvider.Histogram(
		formatkeys(metricConfig.interceptorType, responseSize),
		instrument.WithUnit(unit.Bytes),
	)
	if err != nil {
		return nil, err
	}
	interceptor.requestsPerRPC, err = intProvider.Histogram(
		formatkeys(metricConfig.interceptorType, requestsPerRPC),
		instrument.WithUnit(unit.Dimensionless),
	)
	if err != nil {
		return nil, err
	}
	interceptor.responsesPerRPC, err = intProvider.Histogram(
		formatkeys(metricConfig.interceptorType, responsesPerRPC),
		instrument.WithUnit(unit.Dimensionless),
	)
	if err != nil {
		return nil, err
	}
	interceptor.firstWriteDelay, err = intProvider.Histogram(
		formatkeys(metricConfig.interceptorType, firstWriteDelay),
		instrument.WithUnit(unit.Milliseconds),
	)
	if err != nil {
		return nil, err
	}
	interceptor.interReceiveDuration, err = intProvider.Histogram(
		formatkeys(metricConfig.interceptorType, interReceiveDuration),
		instrument.WithUnit(unit.Milliseconds),
	)
	if err != nil {
		return nil, err
	}
	interceptor.interSendDuration, err = intProvider.Histogram(
		formatkeys(metricConfig.interceptorType, interSendDuration),
		instrument.WithUnit(unit.Milliseconds),
	)
	if err != nil {
		return nil, err
	}
	return &interceptor, nil
}

func (i *metricsInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		requestStartTime := i.now()
		req := &Request{
			Spec:   request.Spec(),
			Peer:   request.Peer(),
			Header: request.Header(),
		}
		if i.config.Filter != nil {
			if !i.config.Filter(ctx, req) {
				return next(ctx, request)
			}
		}
		response, err := next(ctx, request)
		attrs := attributesFromRequest(req)
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
		i.duration.Record(ctx, i.now().Sub(requestStartTime).Milliseconds(), attrs...)
		i.requestsPerRPC.Record(ctx, 1, attrs...)
		i.responsesPerRPC.Record(ctx, 1, attrs...)
		return response, err
	}
}

func (i *metricsInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		req := &Request{
			Spec:   conn.Spec(),
			Peer:   conn.Peer(),
			Header: conn.RequestHeader(),
		}
		if i.config.Filter != nil {
			if !i.config.Filter(ctx, req) {
				return next(ctx, spec)
			}
		}
		requestStartTime := i.now()
		attrs := attributesFromRequest(req)
		var mut sync.Mutex

		var lastReceive, lastSend time.Time
		return &streamingClientInterceptor{
			StreamingClientConn: conn,
			receive: func(msg any, conn connect.StreamingClientConn) error {
				err := conn.Receive(msg)
				mut.Lock()
				defer mut.Unlock()
				if err != nil {
					attrs = append(attrs, statusCodeAttribute(parseProtocol(conn.RequestHeader()), err))
				}
				if msg, ok := msg.(proto.Message); ok {
					size := proto.Size(msg)
					i.requestSize.Record(ctx, int64(size), attrs...)
				}
				if lastReceive.Equal(time.Time{}) {
					i.interReceiveDuration.Record(ctx, int64(time.Since(lastReceive)), attrs...)
				}
				lastReceive = i.now()
				i.requestsPerRPC.Record(ctx, 1, attrs...)
				i.responsesPerRPC.Record(ctx, 1, attrs...)
				return err
			},
			send: func(msg any, conn connect.StreamingClientConn) error {
				err := conn.Send(msg)
				mut.Lock()
				defer mut.Unlock()
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
				lastSend = i.now()
				return nil
			},
		}
	}
}

func (i *metricsInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		req := &Request{
			Spec:   conn.Spec(),
			Peer:   conn.Peer(),
			Header: conn.RequestHeader(),
		}
		if i.config.Filter != nil {
			if !i.config.Filter(ctx, req) {
				return next(ctx, conn)
			}
		}
		requestStartTime := i.now()
		var lastReceive, lastSend time.Time
		var mut sync.Mutex
		attrs := attributesFromRequest(req)
		ret := &streamingHandlerInterceptor{
			StreamingHandlerConn: conn,
			receive: func(msg any, conn connect.StreamingHandlerConn) error {
				err := conn.Receive(msg)
				mut.Lock()
				defer mut.Unlock()
				if err != nil {
					attrs = append(attrs, statusCodeAttribute(parseProtocol(conn.RequestHeader()), err))
				}
				if msg, ok := msg.(proto.Message); ok {
					size := proto.Size(msg)
					i.requestSize.Record(ctx, int64(size), attrs...)
				}
				if !lastReceive.Equal(time.Time{}) {
					i.interReceiveDuration.Record(ctx, int64(time.Since(lastReceive)), attrs...)
				}
				lastReceive = i.now()
				i.requestsPerRPC.Record(ctx, 1, attrs...)
				i.responsesPerRPC.Record(ctx, 1, attrs...)
				return err
			},
			send: func(msg any, conn connect.StreamingHandlerConn) error {
				err := conn.Send(msg)
				mut.Lock()
				defer mut.Unlock()
				if err != nil {
					attrs = append(attrs, statusCodeAttribute(parseProtocol(conn.RequestHeader()), err))
				}
				if !requestStartTime.Equal(time.Time{}) {
					i.firstWriteDelay.Record(ctx, time.Since(requestStartTime).Milliseconds(), attrs...)
					requestStartTime = time.Time{}
				}
				if msg, ok := msg.(proto.Message); ok {
					size := proto.Size(msg)
					i.responseSize.Record(ctx, int64(size), attrs...)
				}
				if !lastSend.Equal(time.Time{}) {
					i.interReceiveDuration.Record(ctx, time.Since(lastSend).Milliseconds(), attrs...)
				}
				lastSend = i.now()
				return err
			},
		}
		return next(ctx, ret)
	}
}

func attributesFromRequest(req *Request) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	host, port, err := net.SplitHostPort(req.Peer.Addr)
	if err == nil {
		attrs = append(attrs,
			semconv.NetPeerNameKey.String(host),
			semconv.NetPeerPortKey.String(port),
		)
	}
	if addr := req.Peer.Addr; addr != "" {
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
	name := strings.TrimLeft(req.Spec.Procedure, "/")
	protocol := parseProtocol(req.Header)
	attrs = append(attrs, semconv.RPCSystemKey.String(protocol))
	attrs = append(attrs, parseProcedure(name)...)
	return attrs
}
