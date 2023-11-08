// Copyright 2022-2023 The Connect Authors
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
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

type state struct {
	call        *Request
	config      config
	instruments *instruments

	// attributes added to the span. Can only be set by the start and end methods.
	attributes      []attribute.KeyValue
	sentCounter     int // Only accessed by the send method.
	receivedCounter int // Only accessed by the receive method.

	// TODO: use sync.OnceValue
	errOnce sync.Once
	err     error
}

func newState(
	call *Request,
	config config,
	instruments *instruments,
) *state {
	return &state{
		call:        call,
		config:      config,
		instruments: instruments,
	}
}

// start a new span and return the context with the span.
// This method must only be called once at the start of the RPC when the
// request headers are available.
func (s *state) start(ctx context.Context, header http.Header) context.Context {
	name := strings.TrimLeft(s.call.Spec.Procedure, "/")
	protocol := protocolToSemConv(s.call.Peer.Protocol)
	carrier := propagation.HeaderCarrier(header)
	spanKind := trace.SpanKindServer
	if s.call.Spec.IsClient {
		spanKind = trace.SpanKindClient
	}
	// Set the span attributes.
	s.attributes = s.config.filterAttribute.filter(s.call, requestAttributes(s.call)...)
	headerAttrs := headerAttributes(protocol, requestKey, header, s.config.requestHeaderKeys)
	traceOpts := []trace.SpanStartOption{
		trace.WithSpanKind(spanKind),
		trace.WithAttributes(s.attributes...),
		trace.WithAttributes(headerAttrs...),
	}
	if !trace.SpanContextFromContext(ctx).IsValid() {
		ctx = s.config.propagator.Extract(ctx, carrier)
		// If on the server and not trusting the remote, start a new root span.
		if !s.call.Spec.IsClient && !s.config.trustRemote {
			traceOpts = append(traceOpts,
				trace.WithNewRoot(),
				trace.WithLinks(trace.LinkFromContext(ctx)),
			)
		}
	}
	// Start the span.
	ctx, _ = s.config.tracer.Start(
		ctx,
		name,
		traceOpts...,
	)
	if s.call.Spec.IsClient {
		// Inject the newly created span into the carrier.
		s.config.propagator.Inject(ctx, carrier)
	}
	return ctx
}

// end the span and record the duration.
// This method must only be called once at the end of the RPC.
func (s *state) end(ctx context.Context, header http.Header, startAt time.Time) {
	protocol := protocolToSemConv(s.call.Peer.Protocol)
	if statusCodeAttribute, ok := statusCodeAttribute(protocol, s.err); ok {
		s.attributes = s.config.filterAttribute.append(
			s.call,
			s.attributes,
			statusCodeAttribute,
		)
	}
	if s.call.Spec.IsClient && s.err != nil {
		// On the client, record the error as a sent message with the
		// error status code attribute.
		s.receive(ctx, s.err)
	}
	headerAttrs := headerAttributes(protocol, responseKey, header, s.config.responseHeaderKeys)
	// End the span.
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(s.attributes...)
	span.SetAttributes(headerAttrs...)
	span.SetStatus(spanStatus(protocol, s.err))
	span.End()
	// Record the duration metric.
	endAt := s.config.now()
	duration := endAt.Sub(startAt).Milliseconds()
	s.instruments.duration.Record(ctx, duration, metric.WithAttributes(s.attributes...))
}

// setError sets the given err once, and is safe to call concurrently.
func (s *state) setError(err error) {
	// Set the error only once.
	s.errOnce.Do(func() {
		s.err = err
	})
}

// receive records the receive metrics and events.
// It is safe to call concurrently with send but not with itself.
func (s *state) receive(ctx context.Context, msg any) {
	s.receivedCounter++
	typ := semconv.MessageTypeReceived
	s.emitEvent(ctx, s.receivedCounter, typ, msg)
}

// send records the send metrics and events.
// It is safe to call concurrently with receive but not with itself.
func (s *state) send(ctx context.Context, msg any) {
	s.sentCounter++
	typ := semconv.MessageTypeSent
	s.emitEvent(ctx, s.sentCounter, typ, msg)
}

func (s *state) emitEvent(ctx context.Context, msgID int, msgType attribute.KeyValue, msg any) {
	protoMsg, isProto := msg.(proto.Message)
	size := proto.Size(protoMsg) // safe to call even if msg is nil
	// Record the message based metrics.
	isRequest := s.call.Spec.IsClient && msgType == semconv.MessageTypeSent ||
		!s.call.Spec.IsClient && msgType == semconv.MessageTypeReceived
	opts := metric.WithAttributes(s.attributes...)
	if isRequest {
		s.instruments.requestSize.Record(ctx, int64(size), opts)
		s.instruments.requestsPerRPC.Record(ctx, 1, opts)
	} else {
		s.instruments.responseSize.Record(ctx, int64(size), opts)
		s.instruments.responsesPerRPC.Record(ctx, 1, opts)
	}
	// Emit a message event if tracing is enabled.
	if s.config.omitTraceEvents {
		return
	}
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	// Allocate the attributes slice with the expected number of attributes.
	attrs := make([]attribute.KeyValue, 0, 3)
	attrs = s.config.filterAttribute.append(
		s.call, attrs,
		msgType, semconv.MessageIDKey.Int(msgID),
	)
	if isProto {
		attrs = s.config.filterAttribute.append(
			s.call, attrs,
			semconv.MessageUncompressedSizeKey.Int(size),
		)
	}
	span.AddEvent("message", trace.WithAttributes(attrs...))
}
