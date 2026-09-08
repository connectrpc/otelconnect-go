// Copyright 2022-2025 The Connect Authors
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
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Interceptor implements [connect.Interceptor] that adds
// OpenTelemetry metrics and tracing to connect handlers and clients.
type Interceptor struct {
	config            config
	clientInstruments instruments
	serverInstruments instruments
}

var _ connect.Interceptor = &Interceptor{}

// NewInterceptor returns an interceptor that implements [connect.Interceptor].
// It adds OpenTelemetry metrics and tracing to connect handlers and clients.
// Use options to configure the interceptor. Any invalid options will cause an
// error to be returned. The interceptor will use the default tracer and meter
// providers. To use a custom tracer or meter provider pass in the
// [WithTracerProvider] or [WithMeterProvider] options. To disable metrics or
// tracing pass in the [WithoutMetrics] or [WithoutTracing] options.
func NewInterceptor(options ...Option) (*Interceptor, error) {
	cfg := config{
		now: time.Now,
		tracer: otel.GetTracerProvider().Tracer(
			instrumentationName,
			trace.WithInstrumentationVersion(semanticVersion),
		),
		propagator: otel.GetTextMapPropagator(),
		meter: otel.GetMeterProvider().Meter(
			instrumentationName,
			metric.WithInstrumentationVersion(semanticVersion)),
	}
	for _, opt := range options {
		opt.apply(&cfg)
	}
	clientInstruments, err := createInstruments(cfg.meter, clientKey, cfg.durationHistogramOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create client instruments: %w", err)
	}
	serverInstruments, err := createInstruments(cfg.meter, serverKey, cfg.durationHistogramOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create server instruments: %w", err)
	}
	return &Interceptor{
		config:            cfg,
		clientInstruments: clientInstruments,
		serverInstruments: serverInstruments,
	}, nil
}

// WrapUnary implements otel tracing and metrics for unary handlers.
func (i *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		requestStartTime := i.config.now()
		if i.config.filter != nil {
			if !i.config.filter(ctx, request.Spec()) {
				return next(ctx, request)
			}
		}
		labeler, found := LabelerFromContext(ctx)
		if !found {
			ctx = ContextWithLabeler(ctx, labeler)
		}
		isClient := request.Spec().IsClient
		name := strings.TrimLeft(request.Spec().Procedure, "/")
		protocol := protocolToSemConv(request.Peer().Protocol, i.config.rpcSystem)
		state := newStreamingState(protocol, request.Spec(), request.Peer(), i.config.filterAttribute, labeler)
		instrumentation := i.getInstruments(isClient)
		carrier := propagation.HeaderCarrier(request.Header())
		spanKind := trace.SpanKindClient
		traceOpts := make([]trace.SpanStartOption, 0, 4)
		traceOpts = append(traceOpts,
			trace.WithAttributes(state.spanAttributes()...),
			trace.WithAttributes(headerAttributes(requestKey, request.Header(), i.config.requestHeaderKeys)...),
		)
		if !isClient {
			spanKind = trace.SpanKindServer
			// if a span already exists in ctx then there must have already been another interceptor
			// that set it, so don't extract from carrier.
			if !trace.SpanContextFromContext(ctx).IsValid() {
				ctx = i.config.propagator.Extract(ctx, carrier)
				if !i.config.trustRemote {
					traceOpts = append(traceOpts,
						trace.WithNewRoot(),
						trace.WithLinks(trace.LinkFromContext(ctx)),
					)
				}
			}
		}
		traceOpts = append(traceOpts, trace.WithSpanKind(spanKind))
		ctx, span := i.config.tracer.Start(
			ctx,
			name,
			traceOpts...,
		)
		defer span.End()
		if isClient {
			i.config.propagator.Inject(ctx, carrier)
		}
		response, err := next(ctx, request)
		state.finish(err)
		if err == nil {
			if span.IsRecording() {
				span.SetAttributes(headerAttributes(responseKey, response.Header(), i.config.responseHeaderKeys)...)
			}
			if !isClient && i.config.propagateResponseHeader {
				responseCarrier := propagation.HeaderCarrier(response.Header())
				i.config.propagator.Inject(ctx, responseCarrier)
			}
		}
		if isClient {
			span.SetStatus(clientSpanStatus(err))
		} else {
			span.SetStatus(serverSpanStatus(err))
		}
		span.SetAttributes(state.spanAttributes()...)
		instrumentation.duration.Record(ctx, i.config.now().Sub(requestStartTime).Seconds(), metric.WithAttributeSet(
			attribute.NewSet(state.metricAttributes()...),
		))
		return response, err
	}
}

// WrapStreamingClient implements otel tracing and metrics for streaming connect clients.
func (i *Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		if i.config.filter != nil {
			if !i.config.filter(ctx, spec) {
				return next(ctx, spec)
			}
		}
		labeler, found := LabelerFromContext(ctx)
		if !found {
			ctx = ContextWithLabeler(ctx, labeler)
		}
		requestStartTime := i.config.now()
		name := strings.TrimLeft(spec.Procedure, "/")
		// Span is closed on context cancelation or when the stream is closed.
		ctx, span := i.config.tracer.Start( //nolint:spancheck
			ctx,
			name,
			trace.WithSpanKind(trace.SpanKindClient),
		)
		conn := next(ctx, spec)
		instrumentation := i.getInstruments(spec.IsClient)
		// inject the newly created span into the carrier
		carrier := propagation.HeaderCarrier(conn.RequestHeader())
		i.config.propagator.Inject(ctx, carrier)
		protocol := protocolToSemConv(conn.Peer().Protocol, i.config.rpcSystem)
		state := newStreamingState(protocol, spec, conn.Peer(), i.config.filterAttribute, labeler)
		var requestOnce sync.Once
		setRequestAttributes := func() {
			if span.IsRecording() {
				span.SetAttributes(
					headerAttributes(
						requestKey,
						conn.RequestHeader(),
						i.config.requestHeaderKeys,
					)...,
				)
			}
		}
		closeSpan := func() {
			requestOnce.Do(setRequestAttributes)
			state.mu.Lock()
			defer state.mu.Unlock()
			// state.error holds the final error, if any: the status attributes
			// for a stream only exist once it has finished.
			state.finish(state.error)
			if span.IsRecording() {
				span.SetAttributes(state.spanAttributes()...)
				span.SetAttributes(headerAttributes(responseKey, conn.ResponseHeader(), i.config.responseHeaderKeys)...)
			}
			span.SetStatus(clientSpanStatus(state.error))
			span.End()
			duration := i.config.now().Sub(requestStartTime).Seconds()
			instrumentation.duration.Record(ctx, duration, metric.WithAttributeSet(
				attribute.NewSet(state.metricAttributes()...),
			))
		}
		stopCtxClose := context.AfterFunc(ctx, closeSpan)
		return &streamingClientInterceptor{ //nolint:spancheck
			StreamingClientConn: conn,
			onClose: func() {
				if stopCtxClose() {
					closeSpan()
				}
			},
			receive: func(msg any, conn connect.StreamingClientConn) error {
				return state.receive(msg, conn)
			},
			send: func(msg any, conn connect.StreamingClientConn) error {
				requestOnce.Do(setRequestAttributes)
				return state.send(msg, conn)
			},
		}
	}
}

// WrapStreamingHandler implements otel tracing and metrics for streaming connect handlers.
func (i *Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		requestStartTime := i.config.now()
		isClient := conn.Spec().IsClient
		instrumentation := i.getInstruments(isClient)
		if i.config.filter != nil {
			if !i.config.filter(ctx, conn.Spec()) {
				return next(ctx, conn)
			}
		}
		labeler, found := LabelerFromContext(ctx)
		if !found {
			ctx = ContextWithLabeler(ctx, labeler)
		}
		name := strings.TrimLeft(conn.Spec().Procedure, "/")
		protocol := protocolToSemConv(conn.Peer().Protocol, i.config.rpcSystem)
		state := newStreamingState(protocol, conn.Spec(), conn.Peer(), i.config.filterAttribute, labeler)
		// extract any request headers into the context
		carrier := propagation.HeaderCarrier(conn.RequestHeader())
		traceOpts := make([]trace.SpanStartOption, 0, 5)
		traceOpts = append(traceOpts,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(state.spanAttributes()...),
			trace.WithAttributes(headerAttributes(requestKey, conn.RequestHeader(), i.config.requestHeaderKeys)...),
		)
		if !trace.SpanContextFromContext(ctx).IsValid() {
			ctx = i.config.propagator.Extract(ctx, carrier)
			if !i.config.trustRemote {
				traceOpts = append(traceOpts,
					trace.WithNewRoot(),
					trace.WithLinks(trace.LinkFromContext(ctx)),
				)
			}
		}
		// start a new span with any trace that is in the context
		ctx, span := i.config.tracer.Start(
			ctx,
			name,
			traceOpts...,
		)
		defer span.End()

		// Inject traceparent into response headers if enabled
		if i.config.propagateResponseHeader {
			responseCarrier := propagation.HeaderCarrier(conn.ResponseHeader())
			i.config.propagator.Inject(ctx, responseCarrier)
		}

		streamingHandler := &streamingHandlerInterceptor{
			StreamingHandlerConn: conn,
			receive: func(msg any, conn connect.StreamingHandlerConn) error {
				return state.receive(msg, conn)
			},
			send: func(msg any, conn connect.StreamingHandlerConn) error {
				return state.send(msg, conn)
			},
		}
		err := next(ctx, streamingHandler)
		state.finish(err)
		if span.IsRecording() {
			span.SetAttributes(state.spanAttributes()...)
			span.SetAttributes(headerAttributes(responseKey, conn.ResponseHeader(), i.config.responseHeaderKeys)...)
		}
		span.SetStatus(serverSpanStatus(err))
		duration := i.config.now().Sub(requestStartTime).Seconds()
		instrumentation.duration.Record(ctx, duration, metric.WithAttributeSet(
			attribute.NewSet(state.metricAttributes()...),
		))
		return err
	}
}

// getInstruments returns the correct instrumentation for the interceptor.
func (i *Interceptor) getInstruments(isClient bool) *instruments {
	if isClient {
		return &i.clientInstruments
	}
	return &i.serverInstruments
}

// protocolToSemConv converts the protocol string to the OpenTelemetry format.
func protocolToSemConv(protocol string, system RPCSystem) string {
	if system != nil {
		// If an explicit system was configured, that overrides the wire protocol.
		return system.protocol()
	}
	switch protocol {
	case grpcwebString, grpcString:
		return grpcProtocol
	case connectString:
		return connectProtocol
	default:
		return protocol
	}
}

func clientSpanStatus(err error) (codes.Code, string) {
	if err == nil {
		return codes.Unset, ""
	}
	if connect.IsNotModifiedError(err) {
		return codes.Unset, ""
	}
	if connectErr := new(connect.Error); errors.As(err, &connectErr) {
		return codes.Error, connectErr.Message()
	}
	return codes.Error, err.Error()
}

func serverSpanStatus(err error) (codes.Code, string) {
	if err == nil {
		return codes.Unset, ""
	}
	if connect.IsNotModifiedError(err) {
		return codes.Unset, ""
	}

	if connectErr := new(connect.Error); errors.As(err, &connectErr) {
		switch connectErr.Code() {
		case connect.CodeUnknown,
			connect.CodeDeadlineExceeded,
			connect.CodeUnimplemented,
			connect.CodeInternal,
			connect.CodeUnavailable,
			connect.CodeDataLoss:
			return codes.Error, connectErr.Message()
		case connect.CodeCanceled,
			connect.CodeInvalidArgument,
			connect.CodeNotFound,
			connect.CodeAlreadyExists,
			connect.CodePermissionDenied,
			connect.CodeResourceExhausted,
			connect.CodeFailedPrecondition,
			connect.CodeAborted,
			connect.CodeOutOfRange,
			connect.CodeUnauthenticated:
			return codes.Unset, ""
		default:
			return codes.Unset, ""
		}
	}

	return codes.Error, err.Error()
}
