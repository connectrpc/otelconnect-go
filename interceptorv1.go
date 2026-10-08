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
	"io"
	"slices"
	"strings"
	"sync"

	connectv1 "connectrpc.com/connect"
	"connectrpc.com/connect/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// Interceptor implements [connectv1.Interceptor] that adds
// OpenTelemetry metrics and tracing to connect-go v1 handlers and clients.
// For connect-go v2, use [NewServerInterceptor] and [NewClientInterceptor].
type Interceptor struct {
	client *interceptor
	server *interceptor
}

var _ connectv1.Interceptor = &Interceptor{}

// NewInterceptor returns an interceptor that implements [connectv1.Interceptor].
// It adds OpenTelemetry metrics and tracing to connect-go v1 handlers and clients.
// Use options to configure the interceptor. Any invalid options will cause an
// error to be returned. The interceptor will use the default tracer and meter
// providers. To use a custom tracer or meter provider pass in the
// [WithTracerProvider] or [WithMeterProvider] options. To disable metrics or
// tracing pass in the [WithoutMetrics] or [WithoutTracing] options.
//
// For connect-go v2, use [NewServerInterceptor] and [NewClientInterceptor].
func NewInterceptor(options ...Option) (*Interceptor, error) {
	client, err := newInterceptor(clientKey, options...)
	if err != nil {
		return nil, err
	}
	server, err := newInterceptor(serverKey, options...)
	if err != nil {
		return nil, err
	}
	return &Interceptor{
		client: client,
		server: server,
	}, nil
}

// WrapUnary implements otel tracing and metrics for unary handlers.
func (i *Interceptor) WrapUnary(next connectv1.UnaryFunc) connectv1.UnaryFunc {
	return func(ctx context.Context, request connectv1.AnyRequest) (connectv1.AnyResponse, error) {
		isClient := request.Spec().IsClient
		side := i.side(isClient)
		requestStartTime := side.config.now()
		spec := specFromV1(request.Spec())
		if side.config.filter != nil {
			if !side.config.filter(ctx, spec) {
				return next(ctx, request)
			}
		}
		labeler, found := LabelerFromContext(ctx)
		if !found {
			ctx = ContextWithLabeler(ctx, labeler)
		}
		name := strings.TrimLeft(spec.Procedure, "/")
		protocol := protocolToSemConv(request.Peer().Protocol, side.config.rpcSystem)
		state := newStreamingStateV1(protocol, spec, isClient, request.Peer(), &side.config, labeler)
		carrier := propagation.HeaderCarrier(request.Header())
		spanKind := trace.SpanKindClient
		traceOpts := make([]trace.SpanStartOption, 0, 4)
		traceOpts = append(traceOpts,
			trace.WithAttributes(state.spanAttributes()...),
			trace.WithAttributes(headerAttributes(requestKey, request.Header(), side.config.requestHeaderKeys)...),
		)
		if !isClient {
			spanKind = trace.SpanKindServer
			// if a span already exists in ctx then there must have already been another interceptor
			// that set it, so don't extract from carrier.
			if !trace.SpanContextFromContext(ctx).IsValid() {
				ctx = side.config.propagator.Extract(ctx, carrier)
				if !side.config.trustRemote {
					traceOpts = append(traceOpts,
						trace.WithNewRoot(),
						trace.WithLinks(trace.LinkFromContext(ctx)),
					)
				}
			}
		}
		traceOpts = append(traceOpts, trace.WithSpanKind(spanKind))
		ctx, span := side.config.tracer.Start(
			ctx,
			name,
			traceOpts...,
		)
		defer span.End()
		if isClient {
			side.config.propagator.Inject(ctx, carrier)
		}
		response, err := next(ctx, request)
		status := statusOfV1(err)
		state.finish(status)
		if err == nil {
			if span.IsRecording() {
				span.SetAttributes(headerAttributes(responseKey, response.Header(), side.config.responseHeaderKeys)...)
			}
			if !isClient && side.config.propagateResponseHeader {
				responseCarrier := propagation.HeaderCarrier(response.Header())
				side.config.propagator.Inject(ctx, responseCarrier)
			}
		}
		if isClient {
			span.SetStatus(clientSpanStatus(status))
		} else {
			span.SetStatus(serverSpanStatus(status))
		}
		span.SetAttributes(state.spanAttributes()...)
		side.instruments.duration.Record(ctx, side.config.now().Sub(requestStartTime).Seconds(), metric.WithAttributeSet(
			attribute.NewSet(state.metricAttributes()...),
		))
		return response, err
	}
}

// WrapStreamingClient implements otel tracing and metrics for streaming connect clients.
func (i *Interceptor) WrapStreamingClient(next connectv1.StreamingClientFunc) connectv1.StreamingClientFunc {
	return func(ctx context.Context, specV1 connectv1.Spec) connectv1.StreamingClientConn {
		side := i.client
		spec := specFromV1(specV1)
		if side.config.filter != nil {
			if !side.config.filter(ctx, spec) {
				return next(ctx, specV1)
			}
		}
		labeler, found := LabelerFromContext(ctx)
		if !found {
			ctx = ContextWithLabeler(ctx, labeler)
		}
		requestStartTime := side.config.now()
		name := strings.TrimLeft(spec.Procedure, "/")
		// Span is closed on context cancelation or when the stream is closed.
		ctx, span := side.config.tracer.Start( //nolint:spancheck
			ctx,
			name,
			trace.WithSpanKind(trace.SpanKindClient),
		)
		conn := next(ctx, specV1)
		// inject the newly created span into the carrier
		carrier := propagation.HeaderCarrier(conn.RequestHeader())
		side.config.propagator.Inject(ctx, carrier)
		protocol := protocolToSemConv(conn.Peer().Protocol, side.config.rpcSystem)
		state := newStreamingStateV1(protocol, spec, true, conn.Peer(), &side.config, labeler)
		var requestOnce sync.Once
		setRequestAttributes := func() {
			if span.IsRecording() {
				span.SetAttributes(
					headerAttributes(
						requestKey,
						conn.RequestHeader(),
						side.config.requestHeaderKeys,
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
			status := statusOfV1(state.error)
			state.finish(status)
			if span.IsRecording() {
				span.SetAttributes(state.spanAttributes()...)
				span.SetAttributes(headerAttributes(responseKey, conn.ResponseHeader(), side.config.responseHeaderKeys)...)
			}
			span.SetStatus(clientSpanStatus(status))
			span.End()
			duration := side.config.now().Sub(requestStartTime).Seconds()
			side.instruments.duration.Record(ctx, duration, metric.WithAttributeSet(
				attribute.NewSet(state.metricAttributes()...),
			))
		}
		stopCtxClose := context.AfterFunc(ctx, closeSpan)
		return &streamingClientInterceptorV1{ //nolint:spancheck
			StreamingClientConn: conn,
			onClose: func() {
				if stopCtxClose() {
					closeSpan()
				}
			},
			receive: func(msg any, conn connectv1.StreamingClientConn) error {
				return state.receive(msg, conn)
			},
			send: func(msg any, conn connectv1.StreamingClientConn) error {
				requestOnce.Do(setRequestAttributes)
				return state.send(msg, conn)
			},
		}
	}
}

// WrapStreamingHandler implements otel tracing and metrics for streaming connect handlers.
func (i *Interceptor) WrapStreamingHandler(next connectv1.StreamingHandlerFunc) connectv1.StreamingHandlerFunc {
	return func(ctx context.Context, conn connectv1.StreamingHandlerConn) error {
		side := i.server
		requestStartTime := side.config.now()
		spec := specFromV1(conn.Spec())
		if side.config.filter != nil {
			if !side.config.filter(ctx, spec) {
				return next(ctx, conn)
			}
		}
		labeler, found := LabelerFromContext(ctx)
		if !found {
			ctx = ContextWithLabeler(ctx, labeler)
		}
		name := strings.TrimLeft(spec.Procedure, "/")
		protocol := protocolToSemConv(conn.Peer().Protocol, side.config.rpcSystem)
		state := newStreamingStateV1(protocol, spec, false, conn.Peer(), &side.config, labeler)
		// extract any request headers into the context
		carrier := propagation.HeaderCarrier(conn.RequestHeader())
		traceOpts := make([]trace.SpanStartOption, 0, 5)
		traceOpts = append(traceOpts,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(state.spanAttributes()...),
			trace.WithAttributes(headerAttributes(requestKey, conn.RequestHeader(), side.config.requestHeaderKeys)...),
		)
		if !trace.SpanContextFromContext(ctx).IsValid() {
			ctx = side.config.propagator.Extract(ctx, carrier)
			if !side.config.trustRemote {
				traceOpts = append(traceOpts,
					trace.WithNewRoot(),
					trace.WithLinks(trace.LinkFromContext(ctx)),
				)
			}
		}
		// start a new span with any trace that is in the context
		ctx, span := side.config.tracer.Start(
			ctx,
			name,
			traceOpts...,
		)
		defer span.End()

		// Inject traceparent into response headers if enabled
		if side.config.propagateResponseHeader {
			responseCarrier := propagation.HeaderCarrier(conn.ResponseHeader())
			side.config.propagator.Inject(ctx, responseCarrier)
		}

		streamingHandler := &streamingHandlerInterceptorV1{
			StreamingHandlerConn: conn,
			receive: func(msg any, conn connectv1.StreamingHandlerConn) error {
				return state.receive(msg, conn)
			},
			send: func(msg any, conn connectv1.StreamingHandlerConn) error {
				return state.send(msg, conn)
			},
		}
		err := next(ctx, streamingHandler)
		status := statusOfV1(err)
		state.finish(status)
		if span.IsRecording() {
			span.SetAttributes(state.spanAttributes()...)
			span.SetAttributes(headerAttributes(responseKey, conn.ResponseHeader(), side.config.responseHeaderKeys)...)
		}
		span.SetStatus(serverSpanStatus(status))
		duration := side.config.now().Sub(requestStartTime).Seconds()
		side.instruments.duration.Record(ctx, duration, metric.WithAttributeSet(
			attribute.NewSet(state.metricAttributes()...),
		))
		return err
	}
}

// side returns the client or server half of the interceptor.
func (i *Interceptor) side(isClient bool) *interceptor {
	if isClient {
		return i.client
	}
	return i.server
}

// specFromV1 converts a connect-go v1 spec to a connect-go v2 spec.
func specFromV1(spec connectv1.Spec) connect.Spec {
	return connect.Spec{
		StreamType:       connect.StreamType(spec.StreamType),
		IdempotencyLevel: connect.IdempotencyLevel(spec.IdempotencyLevel), //nolint:gosec // levels are 0 to 2
		Schema:           spec.Schema,
		Procedure:        spec.Procedure,
	}
}

// statusOfV1 returns the outcome of a connect-go v1 RPC.
func statusOfV1(err error) rpcStatus {
	switch {
	case err == nil:
		return rpcStatus{}
	case connectv1.IsNotModifiedError(err):
		return rpcStatus{err: err, notModified: true}
	}
	if connectErr := new(connectv1.Error); errors.As(err, &connectErr) {
		return rpcStatus{err: err, code: connect.Code(connectErr.Code()), message: connectErr.Message()}
	}
	return rpcStatus{err: err, code: connect.CodeUnknown, message: err.Error()}
}

type streamingStateV1 struct {
	mu              sync.Mutex
	spec            connect.Spec
	attributeFilter attributeFilter
	attributes      []attribute.KeyValue
	peerAttributes  []attribute.KeyValue // spans only, never metrics
	error           error
	labeler         *Labeler
}

func newStreamingStateV1(
	protocol string,
	spec connect.Spec,
	isClient bool,
	peer connectv1.Peer,
	config *config,
	labeler *Labeler,
) *streamingStateV1 {
	attributes := make([]attribute.KeyValue, 0, 6) // 4 max request attrs + 2 status attrs
	attributes = addRequestAttributes(protocol, attributes, spec)
	var peerAttributes []attribute.KeyValue
	if isClient {
		attributes = addAddressAttributes(attributes, peer.Addr, semconv.ServerAddressKey, semconv.ServerPortKey)
	} else if config.serverPeerAttributes {
		peerAttributes = addAddressAttributes(nil, peer.Addr, semconv.NetworkPeerAddressKey, semconv.NetworkPeerPortKey)
	}
	return &streamingStateV1{
		spec:            spec,
		attributeFilter: config.filterAttribute,
		attributes:      config.filterAttribute.filter(spec, attributes...),
		peerAttributes:  config.filterAttribute.filter(spec, peerAttributes...),
		labeler:         labeler,
	}
}

type sendReceiver interface {
	Receive(any) error
	Send(any) error
}

func (s *streamingStateV1) finish(status rpcStatus) {
	s.error = status.err
	s.attributes = append(s.attributes, s.attributeFilter.filter(s.spec,
		addStatusAttributes(nil, status)...,
	)...)
}

func (s *streamingStateV1) spanAttributes() []attribute.KeyValue {
	if len(s.peerAttributes) == 0 {
		return s.attributes
	}
	return slices.Concat(s.attributes, s.peerAttributes)
}

func (s *streamingStateV1) metricAttributes() []attribute.KeyValue {
	if s.labeler == nil {
		return s.attributes
	}
	labelerAttrs := s.labeler.Get()
	if len(labelerAttrs) == 0 {
		return s.attributes
	}
	return slices.Concat(s.attributes, labelerAttrs)
}

func (s *streamingStateV1) receive(msg any, conn sendReceiver) error {
	err := conn.Receive(msg)
	if err != nil && !errors.Is(err, io.EOF) {
		s.mu.Lock()
		s.error = err
		s.mu.Unlock()
	}
	return err
}

func (s *streamingStateV1) send(msg any, conn sendReceiver) error {
	err := conn.Send(msg)
	if err != nil && !errors.Is(err, io.EOF) {
		s.mu.Lock()
		s.error = err
		s.mu.Unlock()
	}
	return err
}

type streamingClientInterceptorV1 struct {
	connectv1.StreamingClientConn

	receive func(any, connectv1.StreamingClientConn) error
	send    func(any, connectv1.StreamingClientConn) error
	onClose func()
}

func (s *streamingClientInterceptorV1) Receive(msg any) error {
	return s.receive(msg, s.StreamingClientConn)
}

func (s *streamingClientInterceptorV1) Send(msg any) error {
	return s.send(msg, s.StreamingClientConn)
}

func (s *streamingClientInterceptorV1) CloseResponse() error {
	err := s.StreamingClientConn.CloseResponse()
	s.onClose()
	return err
}

type streamingHandlerInterceptorV1 struct {
	connectv1.StreamingHandlerConn

	receive func(any, connectv1.StreamingHandlerConn) error
	send    func(any, connectv1.StreamingHandlerConn) error
}

func (p *streamingHandlerInterceptorV1) Receive(msg any) error {
	return p.receive(msg, p.StreamingHandlerConn)
}

func (p *streamingHandlerInterceptorV1) Send(msg any) error {
	return p.send(msg, p.StreamingHandlerConn)
}
