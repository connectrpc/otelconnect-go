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

// Package otelconnect provides OpenTelemetry tracing and metrics for
// [github.com/bufbuild/connect-go] servers and clients.
package otelconnect

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/propagation"

	"github.com/bufbuild/connect-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/global"
	"go.opentelemetry.io/otel/trace"
)

const (
	version             = "0.0.1-dev"
	semanticVersion     = "semver:" + version
	instrumentationName = "github.com/bufbuild/connect-opentelemetry-go"
)

// Request is the information about each RPC available to filter functions. It
// contains the common subset of [connect.AnyRequest],
// [connect.StreamingClientConn], and [connect.StreamingHandlerConn].
type Request struct {
	Spec   connect.Spec
	Peer   connect.Peer
	Header http.Header
}

type config struct {
	filter     func(context.Context, *Request) bool
	meter      metric.Meter
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	now        func() time.Time
}

// WithTelemetry constructs a connect.Option that adds OpenTelemetry metrics
// and tracing to Connect clients and handlers.
func WithTelemetry(options ...Option) connect.Option {
	return connect.WithInterceptors(NewInterceptor(options...))
}

// NewInterceptor constructs and returns OpenTelemetry Interceptors for metrics
// and tracing.
func NewInterceptor(options ...Option) connect.Interceptor {
	cfg := config{
		now: time.Now,
		tracer: otel.GetTracerProvider().Tracer(
			instrumentationName,
			trace.WithInstrumentationVersion(semanticVersion),
		),
		propagator: otel.GetTextMapPropagator(),
		meter: global.MeterProvider().Meter(
			instrumentationName,
			metric.WithInstrumentationVersion(semanticVersion),
		),
	}
	for _, opt := range options {
		opt.apply(&cfg)
	}
	intercept := newInterceptor(cfg)
	return intercept
}
