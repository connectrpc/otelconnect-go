package otelconnect

import (
	"context"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"time"
)

type config struct {
	filter         func(context.Context, *Request) bool
	meter          metric.Meter
	tracerProvider trace.TracerProvider
	propagator     propagation.TextMapPropagator
	now            func() time.Time
}

func (c config) Tracer() trace.Tracer {
	return c.tracerProvider.Tracer(
		instrumentationName,
		trace.WithInstrumentationVersion(semanticVersion),
	)
}
