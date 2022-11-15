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
	"net/netip"
	"strconv"
	"strings"

	"github.com/bufbuild/connect-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

const (
	metricKeyFormat = "rpc.%s.%s"

	// Metrics as defined by https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/metrics/semantic_conventions/rpc-metrics.md
	duration        = "duration"
	requestSize     = "request.size"
	responseSize    = "response.size"
	requestsPerRPC  = "requests_per_rpc"
	responsesPerRPC = "responses_per_rpc"
)

type interceptor struct {
	initialised     bool
	config          config
	duration        syncint64.Histogram
	requestSize     syncint64.Histogram
	responseSize    syncint64.Histogram
	requestsPerRPC  syncint64.Histogram
	responsesPerRPC syncint64.Histogram
}

func newInterceptor(cfg config) *interceptor {
	return &interceptor{
		config: cfg,
	}
}

func (i *interceptor) initInterceptor(isClient bool) error {
	var err error
	i.initialised = true
	interceptorType := "server"
	if isClient {
		interceptorType = "client"
	}
	intProvider := i.config.meter.SyncInt64()
	i.duration, err = intProvider.Histogram(
		formatkeys(interceptorType, duration),
		instrument.WithUnit(unit.Milliseconds),
	)
	if err != nil {
		return interceptorError(err)
	}
	i.requestSize, err = intProvider.Histogram(
		formatkeys(interceptorType, requestSize),
		instrument.WithUnit(unit.Bytes),
	)
	if err != nil {
		return interceptorError(err)
	}
	i.responseSize, err = intProvider.Histogram(
		formatkeys(interceptorType, responseSize),
		instrument.WithUnit(unit.Bytes),
	)
	if err != nil {
		return interceptorError(err)
	}
	i.requestsPerRPC, err = intProvider.Histogram(
		formatkeys(interceptorType, requestsPerRPC),
		instrument.WithUnit(unit.Dimensionless),
	)
	if err != nil {
		return interceptorError(err)
	}
	i.responsesPerRPC, err = intProvider.Histogram(
		formatkeys(interceptorType, responsesPerRPC),
		instrument.WithUnit(unit.Dimensionless),
	)
	if err != nil {
		return interceptorError(err)
	}
	return nil
}

func (i *interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		requestStartTime := i.config.now()
		if !i.initialised {
			// TODO: what does this error look like
			if err := i.initInterceptor(request.Spec().IsClient); err != nil {
				return nil, err
			}
		}
		req := &Request{
			Spec:   request.Spec(),
			Peer:   request.Peer(),
			Header: request.Header(),
		}
		if i.config.filter != nil {
			if !i.config.filter(ctx, req) {
				return next(ctx, request)
			}
		}
		tracer := i.config.Tracer()
		carrier := propagation.HeaderCarrier(request.Header())
		attrs := attributesFromRequest(req)
		name := strings.TrimLeft(request.Spec().Procedure, "/")
		protocol := parseProtocol(request.Header())
		var span trace.Span
		if request.Spec().IsClient {
			ctx, span = tracer.Start(
				ctx,
				name,
				trace.WithSpanKind(trace.SpanKindClient),
				trace.WithAttributes(attrs...),
			)
			i.config.propagator.Inject(ctx, carrier)
		} else {
			ctx = i.config.propagator.Extract(ctx, carrier)
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
		codeAttr := statusCodeAttribute(protocol, err)
		attrs = append(attrs, codeAttr)
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
		span.SetAttributes(codeAttr)
		return response, err
	}
}

// TODO: implement tracing in WrapStreamingClient.
func (i *interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		requestStartTime := i.config.now()
		conn := next(ctx, spec)
		if !i.initialised {
			if err := i.initInterceptor(spec.IsClient); err != nil {
				return &errorStreamingClientInterceptor{
					StreamingClientConn: conn,
					err:                 err,
				}
			}
		}
		req := &Request{
			Spec:   conn.Spec(),
			Peer:   conn.Peer(),
			Header: conn.RequestHeader(),
		}
		if i.config.filter != nil {
			if !i.config.filter(ctx, req) {
				return next(ctx, spec)
			}
		}
		state := streamingState{
			protocol: parseProtocol(req.Header),
			attrs:    attributesFromRequest(req),
		}
		return &streamingClientInterceptor{
			StreamingClientConn: conn,
			requestClosed:       make(chan struct{}),
			responseClosed:      make(chan struct{}),
			onClose: func() {
				i.duration.Record(ctx, i.config.now().Sub(requestStartTime).Milliseconds(), state.attrs...)
			},
			receive: func(msg any, conn connect.StreamingClientConn) error {
				return state.receive(ctx, i, msg, conn)
			},
			send: func(msg any, conn connect.StreamingClientConn) error {
				return state.send(ctx, i, msg, conn)
			},
		}
	}
}

// TODO: implement tracing in WrapStreamingHandler.
func (i *interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		requestStartTime := i.config.now()
		if !i.initialised {
			if err := i.initInterceptor(conn.Spec().IsClient); err != nil {
				return err
			}
		}
		req := &Request{
			Spec:   conn.Spec(),
			Peer:   conn.Peer(),
			Header: conn.RequestHeader(),
		}
		if i.config.filter != nil {
			if !i.config.filter(ctx, req) {
				return next(ctx, conn)
			}
		}
		state := streamingState{
			protocol: parseProtocol(req.Header),
			attrs:    attributesFromRequest(req),
		}
		streamingHandler := &streamingHandlerInterceptor{
			StreamingHandlerConn: conn,
			receive: func(msg any, conn connect.StreamingHandlerConn) error {
				return state.receive(ctx, i, msg, conn)
			},
			send: func(msg any, conn connect.StreamingHandlerConn) error {
				return state.send(ctx, i, msg, conn)
			},
		}
		err := next(ctx, streamingHandler)
		i.duration.Record(ctx, i.config.now().Sub(requestStartTime).Milliseconds(), state.attrs...)
		return err
	}
}

func parseAddress(address string) []attribute.KeyValue {
	host, port, err := net.SplitHostPort(address)
	if err == nil {
		return []attribute.KeyValue{
			semconv.NetPeerNameKey.String(host),
			semconv.NetPeerPortKey.String(port),
		}
	}
	if addrPort, err := netip.ParseAddrPort(address); err != nil {
		return []attribute.KeyValue{
			semconv.NetPeerIPKey.String(addrPort.Addr().String()),
			semconv.NetPeerPortKey.String(strconv.Itoa(int(addrPort.Port()))),
		}
	}
	return []attribute.KeyValue{semconv.NetPeerNameKey.String(address)}
}

func attributesFromRequest(req *Request) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if addr := req.Peer.Addr; addr != "" {
		attrs = append(attrs, parseAddress(addr)...)
	}
	name := strings.TrimLeft(req.Spec.Procedure, "/")
	protocol := parseProtocol(req.Header)
	attrs = append(attrs, semconv.RPCSystemKey.String(protocol))
	attrs = append(attrs, parseProcedure(name)...)
	return attrs
}

func formatkeys(interceptorType string, metricName string) string {
	return fmt.Sprintf(metricKeyFormat, interceptorType, metricName)
}

func interceptorError(err error) error {
	return connect.NewError(connect.CodeInternal, fmt.Errorf("error initialising interceptor: %w", err))
}
