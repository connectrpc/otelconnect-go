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
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
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
