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
	"sync"

	"github.com/bufbuild/connect-go"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	metricKeyFormat = "rpc.%s.%s"

	// Metrics as defined by https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/metrics/semantic_conventions/rpc-metrics.md
	duration        = "duration"
	requestSize     = "request.size"
	responseSize    = "response.size"
	requestsPerRPC  = "requests_per_rpc"
	responsesPerRPC = "responses_per_rpc"

	messageKey = "message"
	serverKey  = "server"
	clientKey  = "client"

	unaryIncrement = 1
)

var (
	messageTypeReceivedAttribute = semconv.MessageTypeKey.String("RECEIVED") //nolint: gochecknoglobals
	messageTypeSentAttribute     = semconv.MessageTypeKey.String("SENT")     //nolint: gochecknoglobals
)

type instruments struct {
	sync.Once

	initErr         error
	duration        syncint64.Histogram
	requestSize     syncint64.Histogram
	responseSize    syncint64.Histogram
	requestsPerRPC  syncint64.Histogram
	responsesPerRPC syncint64.Histogram
}

func (i *instruments) init(meter metric.Meter, isClient bool) {
	i.Do(func() {
		intProvider := meter.SyncInt64()
		interceptorType := serverKey
		if isClient {
			interceptorType = clientKey
		}
		i.duration, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, duration),
			instrument.WithUnit(unit.Milliseconds),
		)
		if i.initErr != nil {
			return
		}
		i.requestSize, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, requestSize),
			instrument.WithUnit(unit.Bytes),
		)
		if i.initErr != nil {
			return
		}
		i.responseSize, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, responseSize),
			instrument.WithUnit(unit.Bytes),
		)
		if i.initErr != nil {
			return
		}
		i.requestsPerRPC, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, requestsPerRPC),
			instrument.WithUnit(unit.Dimensionless),
		)
		if i.initErr != nil {
			return
		}
		i.responsesPerRPC, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, responsesPerRPC),
			instrument.WithUnit(unit.Dimensionless),
		)
	})
}

type interceptor struct {
	config config

	clientInstruments instruments

	handlerInstruments instruments
}

func (i *interceptor) getAndInitInstrument(isClient bool) (*instruments, error) {
	if isClient {
		i.clientInstruments.init(i.config.meter, isClient)
		return &i.clientInstruments, i.clientInstruments.initErr
	}
	i.handlerInstruments.init(i.config.meter, isClient)
	return &i.handlerInstruments, i.handlerInstruments.initErr
}

func newInterceptor(cfg config) *interceptor {
	return &interceptor{
		config: cfg,
	}
}

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

		// Instrumentation setup
		isClient := request.Spec().IsClient
		tracer := i.config.Tracer()
		name := strings.TrimLeft(request.Spec().Procedure, "/")
		protocol := protocolToSemConv(request.Peer().Protocol)
		attributes := requestAttributes(req)

		instrumentation, err := i.getAndInitInstrument(isClient)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		// Tracing setup
		spanKind := trace.SpanKindClient
		requestSpan, responseSpan := messageTypeSentAttribute, messageTypeReceivedAttribute
		if !isClient {
			spanKind = trace.SpanKindServer
			requestSpan, responseSpan = responseSpan, requestSpan
		}

		// Configure context and carrier
		carrier := propagation.HeaderCarrier(request.Header())
		spanCtx := trace.SpanContextFromContext(ctx)
		ctx = trace.ContextWithRemoteSpanContext(ctx, spanCtx)
		ctx = i.config.propagator.Extract(ctx, carrier)
		ctx, span := tracer.Start(
			ctx,
			name,
			trace.WithSpanKind(spanKind),
		)
		i.config.propagator.Inject(ctx, carrier)
		defer span.End()

		requestsize := msgSize(request)

		// Pre request span
		span.AddEvent(messageKey,
			trace.WithAttributes(
				requestSpan,
				semconv.MessageIDKey.Int(unaryIncrement),
				semconv.MessageUncompressedSizeKey.Int(requestsize),
			),
		)

		// Complete request
		response, err := next(ctx, request)

		// Update attributes
		attributes = append(attributes, statusCodeAttribute(protocol, err))
		responseSize := msgSize(response)

		// Record metrics
		instrumentation.duration.Record(ctx, i.config.now().Sub(requestStartTime).Milliseconds(), attributes...)
		instrumentation.requestSize.Record(ctx, int64(requestsize), attributes...)
		instrumentation.requestsPerRPC.Record(ctx, unaryIncrement, attributes...)
		instrumentation.responseSize.Record(ctx, int64(responseSize), attributes...)
		instrumentation.responsesPerRPC.Record(ctx, unaryIncrement, attributes...)

		// Update spans
		span.AddEvent(messageKey,
			trace.WithAttributes(
				responseSpan,
				semconv.MessageIDKey.Int(unaryIncrement),
				semconv.MessageUncompressedSizeKey.Int(responseSize),
			),
		)

		span.SetStatus(spanStatus(err))
		span.SetAttributes(attributes...)
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
		tracer := i.config.Tracer()

		spanCtx := trace.SpanContextFromContext(ctx)
		ctx = i.config.propagator.Extract(ctx, carrier)
		ctx, span := tracer.Start(
			trace.ContextWithRemoteSpanContext(ctx, spanCtx),
			name,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(state.attributes...),
		)
		i.config.propagator.Inject(ctx, carrier)
		return &streamingClientInterceptor{
			StreamingClientConn: conn,
			onClose: func() {
				state.attributes = append(state.attributes, statusCodeAttribute(state.protocol, state.error))
				span.SetAttributes(state.attributes...)
				span.SetStatus(spanStatus(state.error))
				span.End()
				instrumentation.duration.Record(ctx, i.config.now().Sub(requestStartTime).Milliseconds(), state.attributes...)
			},
			receive: func(msg any, conn connect.StreamingClientConn) error {
				size, err := state.receive(msg, conn)
				instrumentation.responseSize.Record(ctx, int64(size), state.attributes...)
				instrumentation.responsesPerRPC.Record(ctx, 1, state.attributes...)
				if !errors.Is(err, io.EOF) {
					span.AddEvent(messageKey,
						trace.WithAttributes(
							messageTypeReceivedAttribute,
							semconv.MessageUncompressedSizeKey.Int(size),
							semconv.MessageIDKey.Int(state.receivedCounter),
						),
					)
				}
				return err
			},
			send: func(msg any, conn connect.StreamingClientConn) error {
				size, err := state.send(msg, conn)
				instrumentation.requestSize.Record(ctx, int64(size), state.attributes...)
				instrumentation.requestsPerRPC.Record(ctx, 1, state.attributes...)
				if !errors.Is(err, io.EOF) {
					span.AddEvent(messageKey,
						trace.WithAttributes(
							messageTypeSentAttribute,
							semconv.MessageUncompressedSizeKey.Int(size),
							semconv.MessageIDKey.Int(state.sentCounter),
						),
					)
				}
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

		tracer := i.config.Tracer()
		spanCtx := trace.SpanContextFromContext(ctx)
		ctx = i.config.propagator.Extract(ctx, carrier)
		ctx, span := tracer.Start(
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
				instrumentation.requestSize.Record(ctx, int64(size), state.attributes...)
				instrumentation.requestsPerRPC.Record(ctx, 1, state.attributes...)
				if !errors.Is(err, io.EOF) {
					span.AddEvent(messageKey,
						trace.WithAttributes(
							messageTypeReceivedAttribute,
							semconv.MessageUncompressedSizeKey.Int(size),
							semconv.MessageIDKey.Int(state.receivedCounter),
						),
					)
				}
				return err
			},
			send: func(msg any, conn connect.StreamingHandlerConn) error {
				size, err := state.send(msg, conn)
				instrumentation.responsesPerRPC.Record(ctx, 1, state.attributes...)
				instrumentation.responseSize.Record(ctx, int64(size), state.attributes...)
				if !errors.Is(err, io.EOF) {
					span.AddEvent(messageKey,
						trace.WithAttributes(
							messageTypeSentAttribute,
							semconv.MessageUncompressedSizeKey.Int(size),
							semconv.MessageIDKey.Int(state.sentCounter),
						),
					)
				}
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
