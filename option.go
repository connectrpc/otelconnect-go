// Copyright 2022-2023 Buf Technologies, Inc.
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

	"connectrpc.com/otelconnect"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// An Option configures the OpenTelemetry instrumentation.
type Option = otelconnect.Option

// WithPropagator configures the instrumentation to use the supplied propagator
// when extracting and injecting trace context. By default, the instrumentation
// uses otel.GetTextMapPropagator().
func WithPropagator(propagator propagation.TextMapPropagator) Option {
	return otelconnect.WithPropagator(propagator)
}

// WithMeterProvider configures the instrumentation to use the supplied [metric.MeterProvider]
// when extracting and injecting trace context. By default, the instrumentation
// uses global.MeterProvider().
func WithMeterProvider(provider metric.MeterProvider) Option {
	return otelconnect.WithMeterProvider(provider)
}

// WithTracerProvider configures the instrumentation to use the supplied
// provider when creating a tracer. By default, the instrumentation
// uses otel.GetTracerProvider().
func WithTracerProvider(provider trace.TracerProvider) Option {
	return otelconnect.WithTracerProvider(provider)
}

// WithFilter configures the instrumentation to emit traces and metrics only
// when the filter function returns true. Filter functions must be safe to call concurrently.
func WithFilter(filter func(context.Context, *Request) bool) Option {
	return otelconnect.WithFilter(filter)
}

// WithoutTracing disables tracing.
func WithoutTracing() Option {
	return otelconnect.WithoutTracing
}

// WithoutMetrics disables metrics.
func WithoutMetrics() Option {
	return otelconnect.WithoutMetrics()
}

// WithAttributeFilter sets the attribute filter for all metrics and trace attributes.
func WithAttributeFilter(filter AttributeFilter) Option {
	return otelconnect.WithAttributeFilter(filter)
}

// WithoutServerPeerAttributes removes net.peer.port and net.peer.name
// attributes from server trace and span attributes. The default behavior
// follows the OpenTelemetry semantic conventions for RPC, but produces very
// high-cardinality data; this option significantly reduces cardinality in most
// environments.
func WithoutServerPeerAttributes() Option {
	return otelconnect.WithoutServerPeerAttributes()
}

// WithTrustRemote sets the Interceptor to trust remote spans.
// By default, all incoming server spans are untrusted and will be linked
// with a [trace.Link] and will not be a child span.
// By default, all client spans are trusted and no change occurs when WithTrustRemote is used.
func WithTrustRemote() Option {
	return otelconnect.WithTrustRemote()
}

// WithTraceRequestHeader enables header attributes for the request header keys provided.
// Attributes will be added as Trace attributes only.
func WithTraceRequestHeader(keys ...string) Option {
	return otelconnect.WithTraceRequestHeader(keys...)
}

// WithTraceResponseHeader enables header attributes for the response header keys provided.
// Attributes will be added as Trace attributes only.
func WithTraceResponseHeader(keys ...string) Option {
	return otelconnect.WithTraceResponseHeader(keys...)
}

// WithoutTraceEvents disables trace events for both unary and streaming
// interceptors. This reduces the quantity of data sent to your tracing system
// by omitting per-message information like message size.
func WithoutTraceEvents() Option {
	return otelconnect.WithoutTraceEvents()
}
