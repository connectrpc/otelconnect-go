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

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
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

func WithMeterProvider(provider metric.MeterProvider) Option {
	return &meterProviderOption{provider: provider}
}

// WithTracerProvider configures the instrumentation to use the supplied
// provider when creating a tracer. By default, the instrumentation uses
// otel.GetTracerProvider().
func WithTracerProvider(provider trace.TracerProvider) Option {
	return &tracerProviderOption{provider}
}

// WithFilter configures the instrumentation to emit traces and metrics only
// when the filter function returns true. filter functions must be safe to call
// concurrently.
func WithFilter(filter func(context.Context, *Request) bool) Option {
	return &filterOption{filter}
}

// WithoutTracing disables tracing.
func WithoutTracing() Option {
	return &disableTraceOption{}
}

// WithoutMetrics disables metrics.
func WithoutMetrics() Option {
	return &disableMetricsOption{}
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
		c.tracerProvider = o.provider
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

type disableTraceOption struct{}

func (o *disableTraceOption) apply(c *config) {
	c.tracerProvider = trace.NewNoopTracerProvider()
}

type disableMetricsOption struct{}

func (o *disableMetricsOption) apply(c *config) {
	WithMeterProvider(metric.NewNoopMeterProvider()).apply(c)
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
