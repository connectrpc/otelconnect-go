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
	"net"
	"strings"
	"sync"
	"time"

	"github.com/bufbuild/connect-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"go.opentelemetry.io/otel/trace"
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

func formatkeys(interceptorType InterceptorType, metricName string) string {
	return fmt.Sprintf(metricKeyFormat, interceptorType, metricName)
}

type config struct {
	DisableMetrics  bool
	Filter          func(context.Context, *Request) bool
	MeterProvider   metric.MeterProvider
	Meter           metric.Meter
	TracerProvider  trace.TracerProvider
	Propagator      propagation.TextMapPropagator
	interceptorType InterceptorType
	now             func() time.Time
}

func (c config) Tracer() trace.Tracer {
	return c.TracerProvider.Tracer(
		instrumentationName,
		trace.WithInstrumentationVersion(semanticVersion),
	)
}

type interceptor struct {
	config               config
	duration             syncint64.Histogram
	requestSize          syncint64.Histogram
	responseSize         syncint64.Histogram
	requestsPerRPC       syncint64.Histogram
	responsesPerRPC      syncint64.Histogram
	firstWriteDelay      syncint64.Histogram
	interReceiveDuration syncint64.Histogram
	interSendDuration    syncint64.Histogram
}

func newInterceptor(cfg config) (*interceptor, error) {
	intercept := interceptor{
		config: cfg,
	}
	var err error
	intProvider := intercept.config.Meter.SyncInt64()
	intercept.duration, err = intProvider.Histogram(
		formatkeys(cfg.interceptorType, duration),
		instrument.WithUnit(unit.Milliseconds),
	)
	if err != nil {
		return nil, err
	}
	intercept.requestSize, err = intProvider.Histogram(
		formatkeys(cfg.interceptorType, requestSize),
		instrument.WithUnit(unit.Bytes),
	)
	if err != nil {
		return nil, err
	}
	intercept.responseSize, err = intProvider.Histogram(
		formatkeys(cfg.interceptorType, responseSize),
		instrument.WithUnit(unit.Bytes),
	)
	if err != nil {
		return nil, err
	}
	intercept.requestsPerRPC, err = intProvider.Histogram(
		formatkeys(cfg.interceptorType, requestsPerRPC),
		instrument.WithUnit(unit.Dimensionless),
	)
	if err != nil {
		return nil, err
	}
	intercept.responsesPerRPC, err = intProvider.Histogram(
		formatkeys(cfg.interceptorType, responsesPerRPC),
		instrument.WithUnit(unit.Dimensionless),
	)
	if err != nil {
		return nil, err
	}
	intercept.firstWriteDelay, err = intProvider.Histogram(
		formatkeys(cfg.interceptorType, firstWriteDelay),
		instrument.WithUnit(unit.Milliseconds),
	)
	if err != nil {
		return nil, err
	}
	intercept.interReceiveDuration, err = intProvider.Histogram(
		formatkeys(cfg.interceptorType, interReceiveDuration),
		instrument.WithUnit(unit.Milliseconds),
	)
	if err != nil {
		return nil, err
	}
	intercept.interSendDuration, err = intProvider.Histogram(
		formatkeys(cfg.interceptorType, interSendDuration),
		instrument.WithUnit(unit.Milliseconds),
	)
	if err != nil {
		return nil, err
	}
	return &intercept, nil
}

func (i *interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		requestStartTime := i.config.now()
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
		tracer := i.config.Tracer()
		var span trace.Span
		carrier := propagation.HeaderCarrier(request.Header())
		name := strings.TrimLeft(request.Spec().Procedure, "/")
		attrs := attributesFromRequest(req)
		if request.Spec().IsClient {
			ctx, span = tracer.Start(
				ctx,
				name,
				trace.WithSpanKind(trace.SpanKindClient),
				trace.WithAttributes(attrs...),
			)
			i.config.Propagator.Inject(ctx, carrier)
		} else {
			ctx = i.config.Propagator.Extract(ctx, carrier)
			spanCtx := trace.SpanContextFromContext(ctx)
			ctx, span = tracer.Start(
				trace.ContextWithRemoteSpanContext(ctx, spanCtx),
				name,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(attrs...),
			)
		}
		defer span.End()
		reqSpanType, resSpanType := semconv.MessageTypeKey.String("RECEIVED"), semconv.MessageTypeKey.String("SENT")
		if request.Spec().IsClient {
			reqSpanType, resSpanType = resSpanType, reqSpanType
		}
		reqSpanAttrs := []attribute.KeyValue{reqSpanType, semconv.MessageIDKey.Int(1)}
		if msg, ok := request.Any().(proto.Message); ok {
			size := proto.Size(msg)
			reqSpanAttrs = append(reqSpanAttrs, semconv.MessageUncompressedSizeKey.Int(size))
		}
		span.AddEvent("message", trace.WithAttributes(reqSpanAttrs...))
		response, err := next(ctx, request)
		protocol := parseProtocol(request.Header())
		attrs = append(attrs, statusCodeAttribute(protocol, err))
		if msg, ok := request.Any().(proto.Message); ok {
			size := proto.Size(msg)
			i.requestSize.Record(ctx, int64(size), attrs...)
		}
		resSpanAttrs := []attribute.KeyValue{resSpanType, semconv.MessageIDKey.Int(1)}
		if err == nil {
			if msg, ok := response.Any().(proto.Message); ok {
				size := proto.Size(msg)
				i.responseSize.Record(ctx, int64(size), attrs...)
				resSpanAttrs = append(resSpanAttrs, semconv.MessageUncompressedSizeKey.Int(size))
			}
		}
		span.AddEvent("message", trace.WithAttributes(resSpanAttrs...))
		i.duration.Record(ctx, i.config.now().Sub(requestStartTime).Milliseconds(), attrs...)
		i.requestsPerRPC.Record(ctx, 1, attrs...)
		i.responsesPerRPC.Record(ctx, 1, attrs...)
		if err != nil {
			if connectErr := new(connect.Error); errors.As(err, &connectErr) {
				span.SetStatus(codes.Error, connectErr.Message())
			} else {
				span.SetStatus(codes.Error, err.Error())
			}
		}
		codeAttr := statusCodeAttribute(parseProtocol(request.Header()), err)
		span.SetAttributes(codeAttr)
		return response, err
	}
}

// TODO: implement tracing in WrapStreamingClient.
func (i *interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
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
		requestStartTime := i.config.now()
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
				lastReceive = i.config.now()
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
				lastSend = i.config.now()
				return nil
			},
		}
	}
}

// TODO: implement tracing in WrapStreamingHandler.
func (i *interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
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
		requestStartTime := i.config.now()
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
				lastReceive = i.config.now()
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
				lastSend = i.config.now()
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
