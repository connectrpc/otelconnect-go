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
	"strings"

	"github.com/bufbuild/connect-go"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

type interceptor struct {
	config             config
	clientInstruments  instruments
	handlerInstruments instruments
}

func newInterceptor(cfg config) *interceptor {
	return &interceptor{
		config: cfg,
	}
}

func (i *interceptor) getAndInitInstrument(isClient bool) (*instruments, error) {
	if isClient {
		i.clientInstruments.init(i.config.meter, isClient)
		return &i.clientInstruments, i.clientInstruments.initErr
	}
	i.handlerInstruments.init(i.config.meter, isClient)
	return &i.handlerInstruments, i.handlerInstruments.initErr
}

// WrapUnary implements otel tracing and metrics for unary handlers.
func (i *interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		requestStartTime := i.config.now()
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
		attributeFilter := i.config.filterAttribute.filter
		isClient := request.Spec().IsClient
		name := strings.TrimLeft(request.Spec().Procedure, "/")
		protocol := protocolToSemConv(request.Peer().Protocol)
		attributes := attributeFilter(req, requestAttributes(req)...)
		instrumentation, err := i.getAndInitInstrument(isClient)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		carrier := propagation.HeaderCarrier(request.Header())
		spanKind := trace.SpanKindClient
		requestSpan, responseSpan := semconv.MessageTypeSent, semconv.MessageTypeReceived
		if !isClient {
			spanKind = trace.SpanKindServer
			requestSpan, responseSpan = semconv.MessageTypeReceived, semconv.MessageTypeSent
			// if a span already exists in ctx then there must have already been another interceptor
			// that set it, so don't extract from carrier.
			if !trace.SpanContextFromContext(ctx).IsValid() {
				ctx = i.config.propagator.Extract(ctx, carrier)
			}
		}
		ctx, span := i.config.tracer.Start(
			ctx,
			name,
			trace.WithSpanKind(spanKind),
			trace.WithAttributes(attributes...),
		)
		defer span.End()
		if isClient {
			i.config.propagator.Inject(ctx, carrier)
		}
		var requestSize int
		if request != nil {
			if msg, ok := request.Any().(proto.Message); ok {
				requestSize = proto.Size(msg)
			}
		}
		span.AddEvent(messageKey,
			trace.WithAttributes(
				requestSpan,
				semconv.MessageIDKey.Int(1),
				semconv.MessageUncompressedSizeKey.Int(requestSize),
			),
		)
		response, err := next(ctx, request)
		attributes = append(attributes, statusCodeAttribute(protocol, err))
		var responseSize int
		if err == nil {
			if msg, ok := response.Any().(proto.Message); ok {
				responseSize = proto.Size(msg)
			}
		}
		span.AddEvent(messageKey,
			trace.WithAttributes(
				responseSpan,
				semconv.MessageIDKey.Int(1),
				semconv.MessageUncompressedSizeKey.Int(responseSize),
			),
		)
		attributes = attributeFilter(req, attributes...)
		span.SetStatus(spanStatus(err))
		span.SetAttributes(attributes...)
		instrumentation.duration.Record(ctx, i.config.now().Sub(requestStartTime).Milliseconds(), attributes...)
		instrumentation.requestSize.Record(ctx, int64(requestSize), attributes...)
		instrumentation.requestsPerRPC.Record(ctx, 1, attributes...)
		instrumentation.responseSize.Record(ctx, int64(responseSize), attributes...)
		instrumentation.responsesPerRPC.Record(ctx, 1, attributes...)
		return response, err
	}
}

// WrapStreamingClient implements otel tracing and metrics for streaming connect clients.
func (i *interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		requestStartTime := i.config.now()
		conn := next(ctx, spec)
		instrumentation, err := i.getAndInitInstrument(spec.IsClient)
		if err != nil {
			return &errorStreamingClientInterceptor{
				StreamingClientConn: conn,
				err:                 connect.NewError(connect.CodeInternal, err),
			}
		}
		req := &Request{
			Spec:   conn.Spec(),
			Peer:   conn.Peer(),
			Header: conn.RequestHeader(),
		}
		if i.config.filter != nil {
			if !i.config.filter(ctx, req) {
				return conn
			}
		}
		attributeFilter := i.config.filterAttribute.filter
		reqAttributes := attributeFilter(req, requestAttributes(req)...)
		name := strings.TrimLeft(conn.Spec().Procedure, "/")
		protocol := protocolToSemConv(conn.Peer().Protocol)
		ctx, span := i.config.tracer.Start(
			ctx,
			name,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(reqAttributes...),
		)
		state := streamingState{
			attributes:      reqAttributes,
			protocol:        protocol,
			req:             req,
			attributeFilter: i.config.filterAttribute,
			span:            span,
			receiveSize:     instrumentation.responseSize,
			receivesPerRPC:  instrumentation.responsesPerRPC,
			sendSize:        instrumentation.requestSize,
			sendsPerRPC:     instrumentation.requestsPerRPC,
		}
		// inject the newly created span into the carrier
		carrier := propagation.HeaderCarrier(conn.RequestHeader())
		i.config.propagator.Inject(ctx, carrier)
		return &streamingClientInterceptor{
			StreamingClientConn: conn,
			onClose: func() {
				// state.attributes is updated with the final error that was recorded.
				// If error is nil a "success" is recorded on the span and on the final duration
				// metric. The "rpc.<protocol>.status_code" is not defined for any other metrics for
				// streams because the error only exists when finishing the stream.
				state.attributes = append(state.attributes, statusCodeAttribute(protocol, state.error))
				state.attributes = attributeFilter(req, state.attributes...)
				span.SetAttributes(state.attributes...)
				span.SetStatus(spanStatus(state.error))
				span.End()
				instrumentation.duration.Record(ctx, i.config.now().Sub(requestStartTime).Milliseconds(), state.attributes...)
			},
			receive: func(msg any, conn connect.StreamingClientConn) error {
				return state.receive(ctx, msg, conn)
			},
			send: func(msg any, conn connect.StreamingClientConn) error {
				return state.send(ctx, msg, conn)
			},
		}
	}
}

// WrapStreamingHandler implements otel tracing and metrics for streaming connect handlers.
func (i *interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		requestStartTime := i.config.now()
		isClient := conn.Spec().IsClient
		instrumentation, err := i.getAndInitInstrument(isClient)
		if err != nil {
			return err
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
		attributeFilter := i.config.filterAttribute.filter
		reqAttributes := attributeFilter(req, requestAttributes(req)...)
		protocol := protocolToSemConv(req.Peer.Protocol)
		name := strings.TrimLeft(conn.Spec().Procedure, "/")
		// extract any request headers into the context
		carrier := propagation.HeaderCarrier(conn.RequestHeader())
		if !trace.SpanContextFromContext(ctx).IsValid() {
			ctx = i.config.propagator.Extract(ctx, carrier)
		}
		// start a new span with any trace that is in the context
		ctx, span := i.config.tracer.Start(
			ctx,
			name,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(reqAttributes...),
		)
		defer span.End()
		state := streamingState{
			attributes:      reqAttributes,
			protocol:        protocol,
			req:             req,
			attributeFilter: i.config.filterAttribute,
			span:            span,
			receiveSize:     instrumentation.requestSize,
			receivesPerRPC:  instrumentation.requestsPerRPC,
			sendSize:        instrumentation.responseSize,
			sendsPerRPC:     instrumentation.responsesPerRPC,
		}
		streamingHandler := &streamingHandlerInterceptor{
			StreamingHandlerConn: conn,
			receive: func(msg any, conn connect.StreamingHandlerConn) error {
				return state.receive(ctx, msg, conn)
			},
			send: func(msg any, conn connect.StreamingHandlerConn) error {
				return state.send(ctx, msg, conn)
			},
		}
		err = next(ctx, streamingHandler)
		state.attributes = append(state.attributes, statusCodeAttribute(protocol, err))
		state.attributes = attributeFilter(req, state.attributes...)
		span.SetAttributes(state.attributes...)
		span.SetStatus(spanStatus(err))
		instrumentation.duration.Record(ctx, i.config.now().Sub(requestStartTime).Milliseconds(), state.attributes...)
		return err
	}
}

func protocolToSemConv(protocol string) string {
	switch protocol {
	case "grpcweb":
		return "grpc_web"
	case "grpc":
		return "grpc"
	case "connect":
		return "buf_connect"
	default:
		return protocol
	}
}

func spanStatus(err error) (codes.Code, string) {
	if err == nil {
		return codes.Ok, ""
	}
	if connectErr := new(connect.Error); errors.As(err, &connectErr) {
		return codes.Error, connectErr.Message()
	}
	return codes.Error, err.Error()
}
