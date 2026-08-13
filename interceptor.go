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

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// interceptor bundles the configuration and OpenTelemetry instruments for
// one side of an RPC.
type interceptor struct {
	config      config
	instruments instruments
}

// NewServerInterceptor returns a [connect.ServerInterceptor] that adds
// OpenTelemetry metrics and tracing to connect handlers. Use options to
// configure the interceptor. Any invalid options will cause an error to be
// returned. The interceptor will use the default tracer and meter providers.
// To use a custom tracer or meter provider pass in the [WithTracerProvider]
// or [WithMeterProvider] options. To disable metrics or tracing pass in the
// [WithoutMetrics] or [WithoutTracing] options.
func NewServerInterceptor(options ...Option) (connect.ServerInterceptor, error) {
	intercept, err := newInterceptor(serverKey, options...)
	if err != nil {
		return nil, err
	}
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			return intercept.serveServer(ctx, spec, stream, next)
		}
	}, nil
}

// NewClientInterceptor returns a [connect.ClientInterceptor] that adds
// OpenTelemetry metrics and tracing to connect clients. Use options to
// configure the interceptor. Any invalid options will cause an error to be
// returned. The interceptor will use the default tracer and meter providers.
// To use a custom tracer or meter provider pass in the [WithTracerProvider]
// or [WithMeterProvider] options. To disable metrics or tracing pass in the
// [WithoutMetrics] or [WithoutTracing] options.
func NewClientInterceptor(options ...Option) (connect.ClientInterceptor, error) {
	intercept, err := newInterceptor(clientKey, options...)
	if err != nil {
		return nil, err
	}
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			return intercept.serveClient(ctx, spec, next)
		}
	}, nil
}

// newInterceptor applies options and builds the instruments for the named
// side (serverKey or clientKey).
func newInterceptor(side string, options ...Option) (*interceptor, error) {
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
	sideInstruments, err := createInstruments(cfg.meter, side)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s instruments: %w", side, err)
	}
	return &interceptor{
		config:      cfg,
		instruments: sideInstruments,
	}, nil
}

// serveServer implements otel tracing and metrics for connect handlers.
// Unary and streaming RPCs both flow through this method.
func (i *interceptor) serveServer(ctx context.Context, spec connect.Spec, stream connect.ServerStream, next connect.ServerFunc) error {
	requestStartTime := i.config.now()
	if i.config.filter != nil {
		if !i.config.filter(ctx, spec) {
			return next(ctx, spec, stream)
		}
	}
	labeler, found := LabelerFromContext(ctx)
	if !found {
		ctx = ContextWithLabeler(ctx, labeler)
	}
	callInfo, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		callInfo = &connect.CallInfo{}
	}
	name := strings.TrimLeft(spec.Procedure, "/")
	protocol := protocolToSemConv(callInfo.Protocol, i.config.rpcSystem)
	state := newStreamingState(
		protocol,
		spec,
		callInfo.PeerAddr,
		i.config.filterAttribute,
		i.config.omitTraceEvents,
		i.instruments.requestSize,
		i.instruments.responseSize,
		labeler,
	)
	// extract any request headers into the context
	carrier := metadataCarrier{m: callInfo.RequestHeader()}
	traceOpts := make([]trace.SpanStartOption, 0, 5)
	traceOpts = append(traceOpts,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(state.attributes...),
		trace.WithAttributes(headerAttributes(protocol, requestKey, callInfo.RequestHeader(), i.config.requestHeaderKeys)...),
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
		responseCarrier := metadataCarrier{m: callInfo.ResponseHeader()}
		i.config.propagator.Inject(ctx, responseCarrier)
	}

	streamingHandler := &streamingHandlerInterceptor{
		ServerStream: stream,
		receive: func(msg any, stream connect.ServerStream) error {
			return state.receive(ctx, msg, stream)
		},
		send: func(msg any, stream connect.ServerStream) error {
			return state.send(ctx, msg, stream)
		},
	}
	err := next(ctx, spec, streamingHandler)
	if statusCode, ok := statusCodeAttribute(protocol, err); ok {
		state.addAttributes(statusCode)
	}
	if span.IsRecording() {
		span.SetAttributes(state.attributes...)
		span.SetAttributes(headerAttributes(protocol, responseKey, callInfo.ResponseHeader(), i.config.responseHeaderKeys)...)
	}
	span.SetStatus(serverSpanStatus(protocol, err))
	attributeSet := attribute.NewSet(state.metricAttributes()...)
	i.instruments.requestsPerRPC.Record(ctx, state.receivedCounter, metric.WithAttributeSet(attributeSet))
	i.instruments.responsesPerRPC.Record(ctx, state.sentCounter, metric.WithAttributeSet(attributeSet))
	duration := i.config.now().Sub(requestStartTime).Milliseconds()
	i.instruments.duration.Record(ctx, duration, metric.WithAttributeSet(attributeSet))
	return err
}

// serveClient implements otel tracing and metrics for connect clients.
// Unary and streaming RPCs both flow through this method: next opens the
// stream and the returned wrapper meters every Send and Receive.
func (i *interceptor) serveClient(ctx context.Context, spec connect.Spec, next connect.ClientFunc) (connect.ClientStream, error) {
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
	callInfo, ok := connect.CallInfoForClientContext(ctx)
	if !ok {
		ctx, callInfo = connect.NewClientContext(ctx)
	}
	// Span is closed on context cancelation or when the stream is closed.
	ctx, span := i.config.tracer.Start( //nolint:spancheck
		ctx,
		name,
		trace.WithSpanKind(trace.SpanKindClient),
	)
	// inject the newly created span into the carrier
	carrier := metadataCarrier{m: callInfo.RequestHeader()}
	i.config.propagator.Inject(ctx, carrier)
	conn, err := next(ctx, spec)
	protocol := protocolToSemConv(callInfo.Protocol, i.config.rpcSystem)
	state := newStreamingState(
		protocol,
		spec,
		callInfo.PeerAddr,
		i.config.filterAttribute,
		i.config.omitTraceEvents,
		i.instruments.responseSize,
		i.instruments.requestSize,
		labeler,
	)
	var requestOnce sync.Once
	setRequestAttributes := func() {
		if span.IsRecording() {
			span.SetAttributes(
				headerAttributes(
					protocol,
					requestKey,
					callInfo.RequestHeader(),
					i.config.requestHeaderKeys,
				)...,
			)
		}
	}
	closeSpan := func() {
		requestOnce.Do(setRequestAttributes)
		state.mu.Lock()
		defer state.mu.Unlock()
		// state.attributes is updated with the final error that was recorded.
		// If error is nil a "success" is recorded on the span and on the final duration
		// metric. The "rpc.<protocol>.status_code" is not defined for any other metrics for
		// streams because the error only exists when finishing the stream.
		if statusCode, ok := statusCodeAttribute(protocol, state.error); ok {
			state.addAttributes(statusCode)
		}
		if span.IsRecording() {
			span.SetAttributes(state.attributes...)
			span.SetAttributes(headerAttributes(protocol, responseKey, callInfo.ResponseHeader(), i.config.responseHeaderKeys)...)
		}
		span.SetStatus(clientSpanStatus(protocol, state.error))
		span.End()
		attributeSet := attribute.NewSet(state.metricAttributes()...)
		i.instruments.requestsPerRPC.Record(ctx, state.sentCounter, metric.WithAttributeSet(attributeSet))
		i.instruments.responsesPerRPC.Record(ctx, state.receivedCounter, metric.WithAttributeSet(attributeSet))
		duration := i.config.now().Sub(requestStartTime).Milliseconds()
		i.instruments.duration.Record(ctx, duration, metric.WithAttributeSet(attributeSet))
	}
	if err != nil {
		// The transport failed to open the stream, so there is no Close to
		// hook: record the error and finalize now.
		state.error = err
		closeSpan()
		return nil, err //nolint:spancheck // closeSpan ends the span.
	}
	stopCtxClose := context.AfterFunc(ctx, closeSpan)
	return &streamingClientInterceptor{
		ClientStream: conn,
		onClose: func() {
			if stopCtxClose() {
				closeSpan()
			}
		},
		receive: func(msg any, conn connect.ClientStream) error {
			return state.receive(ctx, msg, conn)
		},
		send: func(msg any, conn connect.ClientStream) error {
			requestOnce.Do(setRequestAttributes)
			return state.send(ctx, msg, conn)
		},
	}, nil
}

// protocolToSemConv converts the protocol string to the OpenTelemetry format.
func protocolToSemConv(protocol string, system RPCSystem) string {
	if system != nil {
		return system.protocol()
	}
	switch protocol {
	case connect.ProtocolNameGRPCWeb, connect.ProtocolNameGRPC:
		return grpcProtocol
	case connect.ProtocolNameConnect:
		return connectProtocol
	default:
		return protocol
	}
}

func clientSpanStatus(protocol string, err error) (codes.Code, string) {
	if err == nil {
		return codes.Unset, ""
	}
	if protocol == connectProtocol && connecthttp.IsNotModifiedError(err) {
		return codes.Unset, ""
	}
	if connectErr := new(connect.Error); errors.As(err, &connectErr) {
		return codes.Error, connectErr.Message()
	}
	return codes.Error, err.Error()
}

func serverSpanStatus(protocol string, err error) (codes.Code, string) {
	if err == nil {
		return codes.Unset, ""
	}
	if protocol == connectProtocol && connecthttp.IsNotModifiedError(err) {
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
		}
	}

	return codes.Error, err.Error()
}

// metadataCarrier adapts a [*connect.Header] to OpenTelemetry's
// [propagation.TextMapCarrier] for trace-context propagation.
type metadataCarrier struct {
	m *connect.Header
}

func (c metadataCarrier) Get(key string) string {
	return c.m.Get(key)
}

func (c metadataCarrier) Set(key, value string) { c.m.Set(key, value) }

func (c metadataCarrier) Keys() []string {
	keys := make([]string, 0, c.m.Len())
	for key := range c.m.All() {
		keys = append(keys, key)
	}
	return keys
}
