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
	"net"
	"strings"

	"github.com/bufbuild/connect-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

type traceConfig struct {
	Filter     func(context.Context, *Request) bool
	Provider   trace.TracerProvider
	Propagator propagation.TextMapPropagator
}

func (c traceConfig) Tracer() trace.Tracer {
	return c.Provider.Tracer(
		instrumentationName,
		trace.WithInstrumentationVersion(semanticVersion),
	)
}

type traceInterceptor struct {
	config traceConfig
}

func (i *traceInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return connect.UnaryFunc(func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		// TODO: I've intentionally minimized abstraction here, so that it's easy
		// to read the logic from top to bottom. (otelgrpc has a bunch of
		// indirection that makes the logic difficult for me to follow.) Once we
		// have tests verifying that this is correct, we can factor out code that
		// ought to be shared with WrapStreamingClient and WrapStreamingHandler.
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
		name := strings.TrimLeft(request.Spec().Procedure, "/")
		protocol := parseProtocol(request.Header())
		attrs := []attribute.KeyValue{semconv.RPCSystemKey.String(protocol)}
		attrs = append(attrs, parseProcedure(name)...)
		if addr := request.Peer().Addr; addr != "" {
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
		tracer := i.config.Tracer()
		var span trace.Span
		carrier := propagation.HeaderCarrier(request.Header())
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
		resSpanAttrs := []attribute.KeyValue{resSpanType, semconv.MessageIDKey.Int(1)}
		if err == nil {
			if msg, ok := response.Any().(proto.Message); ok {
				size := proto.Size(msg)
				resSpanAttrs = append(resSpanAttrs, semconv.MessageUncompressedSizeKey.Int(size))
			}
		}
		span.AddEvent("message", trace.WithAttributes(resSpanAttrs...))
		if err != nil {
			if connectErr := new(connect.Error); errors.As(err, &connectErr) {
				span.SetStatus(codes.Error, connectErr.Message())
			} else {
				span.SetStatus(codes.Error, err.Error())
			}
		}
		// Following the respective specifications, use integers for gRPC codes and
		// strings for Connect codes.
		codeKey := attribute.Key("rpc." + protocol + ".status_code")
		if strings.HasPrefix(protocol, "grpc") {
			if err != nil {
				span.SetAttributes(codeKey.Int64(int64(connect.CodeOf(err))))
			} else {
				span.SetAttributes(codeKey.Int64(0)) // gRPC uses 0 for success
			}
		} else if err != nil {
			span.SetAttributes(codeKey.String(connect.CodeOf(err).String()))
		} else {
			span.SetAttributes(codeKey.String("success"))
		}
		return response, err
	})
}

func (i *traceInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	// TODO: implement. This will require creating a span (as we do for unary),
	// then using it in a struct that implements connect.StreamingClientConn. The
	// StreamingClientConn should end the span once both CloseRequest and
	// CloseResponse have been called.
	return next
}

func (i *traceInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	// TODO: implement, same notes as WrapStreamingClient.
	return next
}
