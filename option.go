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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"go.opentelemetry.io/otel/trace"
)

// An Option configures the OpenTelemetry instrumentation.
type Option interface {
	apply(*config)
}

// WithPropagator configures the instrumentation to use the supplied propagator
// when extracting and injecting trace context. By default, the instrumentation
// uses otel.GetTextMapPropagator().
func WithPropagator(propagator propagation.TextMapPropagator) Option {
	return &propagatorOption{propagator}
}

// WithMeterProvider configures the instrumentation to use the supplied [metric.MeterProvider]
// when extracting and injecting trace context. By default, the instrumentation
// uses global.MeterProvider().
func WithMeterProvider(provider metric.MeterProvider) Option {
	return &meterProviderOption{provider: provider}
}

// WithTracerProvider configures the instrumentation to use the supplied
// provider when creating a tracer. By default, the instrumentation
// uses otel.GetTracerProvider().
func WithTracerProvider(provider trace.TracerProvider) Option {
	return &tracerProviderOption{provider}
}

// WithFilter configures the instrumentation to emit traces and metrics only
// when the filter function returns true. Filter functions must be safe to call concurrently.
func WithFilter(filter func(context.Context, *Request) bool) Option {
	return &filterOption{filter}
}

// WithoutTracing disables tracing.
func WithoutTracing() Option {
	return WithTracerProvider(trace.NewNoopTracerProvider())
}

// WithoutMetrics disables metrics.
func WithoutMetrics() Option {
	return WithMeterProvider(metric.NewNoopMeterProvider())
}

// WithAttributeFilter sets the attribute filter for all metrics and trace attributes.
func WithAttributeFilter(filter AttributeFilter) Option {
	return &attributeFilterOption{filterAttribute: filter}
}

// WithoutServerNetPeer removes net.peer.port and net.peer.name attributes from server trace and span attributes.
// This is recommended to use as net.peer server attributes have high cardinality.
func WithoutServerNetPeer() Option {
	return &withoutServerNetPeerOption{}
}

type withoutServerNetPeerOption struct{}

func (o *withoutServerNetPeerOption) apply(c *config) {
	c.filterAttribute = func(request *Request, value attribute.KeyValue) bool {
		if request.Spec.IsClient {
			return true
		}
		if value.Key == semconv.NetPeerPortKey {
			return false
		}
		if value.Key == semconv.NetPeerNameKey {
			return false
		}
		return true
	}
}

type attributeFilterOption struct {
	filterAttribute AttributeFilter
}

func (o *attributeFilterOption) apply(c *config) {
	if o.filterAttribute != nil {
		c.filterAttribute = o.filterAttribute
	}
}

type propagatorOption struct {
	propagator propagation.TextMapPropagator
}

func (o *propagatorOption) apply(c *config) {
	if o.propagator != nil {
		c.propagator = o.propagator
	}
}

type tracerProviderOption struct {
	provider trace.TracerProvider
}

func (o *tracerProviderOption) apply(c *config) {
	if o.provider != nil {
		c.tracer = o.provider.Tracer(
			instrumentationName,
			trace.WithInstrumentationVersion(semanticVersion),
		)
	}
}

type filterOption struct {
	filter func(context.Context, *Request) bool
}

func (o *filterOption) apply(c *config) {
	if o.filter != nil {
		c.filter = o.filter
	}
}

type meterProviderOption struct {
	provider metric.MeterProvider
}

func (m meterProviderOption) apply(c *config) {
	c.meter = m.provider.Meter(
		instrumentationName,
		metric.WithInstrumentationVersion(semanticVersion),
	)
}
