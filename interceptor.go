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
	"io"
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
		isClient := request.Spec().IsClient
		name := strings.TrimLeft(request.Spec().Procedure, "/")
		protocol := protocolToSemConv(request.Peer().Protocol)
		attributes := requestAttributes(req)
		instrumentation, err := i.getAndInitInstrument(isClient)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		spanKind := trace.SpanKindClient
		requestSpan, responseSpan := semconv.MessageTypeSent, semconv.MessageTypeReceived
		if !isClient {
			spanKind = trace.SpanKindServer
			requestSpan, responseSpan = responseSpan, requestSpan
		}
		carrier := propagation.HeaderCarrier(request.Header())
		spanCtx := trace.SpanContextFromContext(ctx)
		ctx = trace.ContextWithRemoteSpanContext(ctx, spanCtx)
		ctx = i.config.propagator.Extract(ctx, carrier)
		ctx, span := i.config.tracer.Start(
			ctx,
			name,
			trace.WithSpanKind(spanKind),
		)
		i.config.propagator.Inject(ctx, carrier)
		defer span.End()
		requestSize := msgSize(request)
		span.AddEvent(messageKey,
			trace.WithAttributes(
				requestSpan,
				semconv.MessageIDKey.Int(1),
				semconv.MessageUncompressedSizeKey.Int(requestSize),
			),
		)
		response, err := next(ctx, request)
		attributes = append(attributes, statusCodeAttribute(protocol, err))
		responseSize := msgSize(response)
		span.AddEvent(messageKey,
			trace.WithAttributes(
				responseSpan,
				semconv.MessageIDKey.Int(1),
				semconv.MessageUncompressedSizeKey.Int(responseSize),
			),
		)
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
		state := streamingState{
			protocol:   protocolToSemConv(conn.Peer().Protocol),
			attributes: requestAttributes(req),
		}
		carrier := propagation.HeaderCarrier(conn.RequestHeader())
		name := strings.TrimLeft(conn.Spec().Procedure, "/")
		spanCtx := trace.SpanContextFromContext(ctx)
		ctx = i.config.propagator.Extract(ctx, carrier)
		ctx, span := i.config.tracer.Start(
			trace.ContextWithRemoteSpanContext(ctx, spanCtx),
			name,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(state.attributes...),
		)
		i.config.propagator.Inject(ctx, carrier)
		return &streamingClientInterceptor{
			StreamingClientConn: conn,
			onClose: func() {
				// state.attributes is updated with the final error that was recorded.
				// If error is nil a "success" is recorded on the span and on the final duration
				// metric. The "rpc.<protocol>.status_code" is not defined for any other metrics for
				// streams because the error only exists when finishing the stream.
				state.attributes = append(state.attributes, statusCodeAttribute(state.protocol, state.error))
				span.SetAttributes(state.attributes...)
				span.SetStatus(spanStatus(state.error))
				span.End()
				instrumentation.duration.Record(ctx, i.config.now().Sub(requestStartTime).Milliseconds(), state.attributes...)
			},
			receive: func(msg any, conn connect.StreamingClientConn) error {
				size, err := state.receive(msg, conn)
				if errors.Is(err, io.EOF) {
					return err
				}
				if err != nil {
					// If error add it to the attributes because the stream is about to terminate.
					// If no error don't add anything because status only exists at end of stream.
					state.attributes = append(state.attributes, statusCodeAttribute(state.protocol, err))
				}
				span.AddEvent(messageKey,
					trace.WithAttributes(
						semconv.MessageTypeReceived,
						semconv.MessageUncompressedSizeKey.Int(size),
						semconv.MessageIDKey.Int(state.receivedCounter),
					),
				)
				// In WrapStreamingClient the 'receive' is a response message.
				instrumentation.responseSize.Record(ctx, int64(size), state.attributes...)
				instrumentation.responsesPerRPC.Record(ctx, 1, state.attributes...)
				return err
			},
			send: func(msg any, conn connect.StreamingClientConn) error {
				size, err := state.send(msg, conn)
				if errors.Is(err, io.EOF) {
					return err
				}
				if err != nil {
					// If error add it to the attributes because the stream is about to terminate.
					// If no error don't add anything because status only exists at end of stream.
					state.attributes = append(state.attributes, statusCodeAttribute(state.protocol, err))
				}
				span.AddEvent(messageKey,
					trace.WithAttributes(
						semconv.MessageTypeSent,
						semconv.MessageUncompressedSizeKey.Int(size),
						semconv.MessageIDKey.Int(state.sentCounter),
					),
				)
				// In WrapStreamingClient the 'send' is a request message.
				instrumentation.requestSize.Record(ctx, int64(size), state.attributes...)
				instrumentation.requestsPerRPC.Record(ctx, 1, state.attributes...)
				return err
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
		state := streamingState{
			protocol:   protocolToSemConv(req.Peer.Protocol),
			attributes: requestAttributes(req),
		}
		carrier := propagation.HeaderCarrier(conn.RequestHeader())
		name := strings.TrimLeft(conn.Spec().Procedure, "/")
		spanCtx := trace.SpanContextFromContext(ctx)
		ctx = i.config.propagator.Extract(ctx, carrier)
		ctx, span := i.config.tracer.Start(
			trace.ContextWithRemoteSpanContext(ctx, spanCtx),
			name,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(state.attributes...),
		)
		defer span.End()
		i.config.propagator.Inject(ctx, carrier)
		streamingHandler := &streamingHandlerInterceptor{
			StreamingHandlerConn: conn,
			receive: func(msg any, conn connect.StreamingHandlerConn) error {
				size, err := state.receive(msg, conn)
				if errors.Is(err, io.EOF) {
					return err
				}
				if err != nil {
					// If error add it to the attributes because the stream is about to terminate.
					// If no error don't add anything because status only exists at end of stream.
					state.attributes = append(state.attributes, statusCodeAttribute(state.protocol, err))
				}
				span.AddEvent(messageKey,
					trace.WithAttributes(
						semconv.MessageTypeReceived,
						semconv.MessageUncompressedSizeKey.Int(size),
						semconv.MessageIDKey.Int(state.receivedCounter),
					),
				)
				// In WrapStreamingHandler the 'receive' is a request message.
				instrumentation.requestSize.Record(ctx, int64(size), state.attributes...)
				instrumentation.requestsPerRPC.Record(ctx, 1, state.attributes...)
				return err
			},
			send: func(msg any, conn connect.StreamingHandlerConn) error {
				size, err := state.send(msg, conn)
				if errors.Is(err, io.EOF) {
					return err
				}
				if err != nil {
					// If error add it to the attributes because the stream is about to terminate.
					// If no error don't add anything because status only exists at end of stream.
					state.attributes = append(state.attributes, statusCodeAttribute(state.protocol, err))
				}
				span.AddEvent(messageKey,
					trace.WithAttributes(
						semconv.MessageTypeSent,
						semconv.MessageUncompressedSizeKey.Int(size),
						semconv.MessageIDKey.Int(state.sentCounter),
					),
				)
				// In WrapStreamingHandler the 'send' is a response message.
				instrumentation.responsesPerRPC.Record(ctx, 1, state.attributes...)
				instrumentation.responseSize.Record(ctx, int64(size), state.attributes...)
				return err
			},
		}
		err = next(ctx, streamingHandler)
		state.attributes = append(state.attributes, statusCodeAttribute(state.protocol, err))
		span.SetAttributes(state.attributes...)
		span.SetStatus(spanStatus(err))
		instrumentation.duration.Record(ctx, i.config.now().Sub(requestStartTime).Milliseconds(), state.attributes...)
		return err
	}
}

func protocolToSemConv(peer string) string {
	switch peer {
	case "grpcweb":
		return "grpc_web"
	case "grpc":
		return "grpc"
	case "connect":
		return "buf_connect"
	default:
		return peer
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

type anyer interface {
	Any() any
}

func msgSize(msg anyer) int {
	if msg == nil {
		return 0
	}
	if msg, ok := msg.Any().(proto.Message); ok {
		return proto.Size(msg)
	}
	return 0
}
