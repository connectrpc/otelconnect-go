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

package otelconnect_test

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/bufbuild/connect-go"
	otelconnect "github.com/bufbuild/connect-opentelemetry-go"
	pingv1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1"
	"github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1/pingv1connect"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
)

// Example_serverOptions shows how OpenTelemetry propagators, meter providers and trace providers
// can be set using [WithMeterProvider], [WithPropagator], and [WithTracerProvider] instead of using otel globals.
func Example_serverOptions() {
	mux := http.NewServeMux()
	mux.Handle(
		pingv1connect.NewPingServiceHandler(
			&pingv1connect.UnimplementedPingServiceHandler{}, // Or use custom implementation here
			otelconnect.WithTelemetry( // Set providers and propagator instead of using globals
				otelconnect.WithTracerProvider(trace.NewTracerProvider()),
				otelconnect.WithMeterProvider(metric.NewMeterProvider()),
				otelconnect.WithPropagator(propagation.TraceContext{}),
			),
		),
	)
	log.Fatal(http.ListenAndServe(":8080", mux))
}

// Example_clientOptions shows how OpenTelemetry propagators, meter providers and trace providers
// can be set using [WithMeterProvider], [WithPropagator], and [WithTracerProvider] instead of using otel globals.
func Example_clientOptions() {
	client := pingv1connect.NewPingServiceClient(
		http.DefaultClient,
		"http://localhost:8080",
		otelconnect.WithTelemetry( // Set providers and propagator instead of using globals
			otelconnect.WithTracerProvider(trace.NewTracerProvider()),
			otelconnect.WithMeterProvider(metric.NewMeterProvider()),
			otelconnect.WithPropagator(propagation.TraceContext{}),
		),
	)
	resp, err := client.Ping(
		context.Background(),
		connect.NewRequest(&pingv1.PingRequest{}),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(resp.Msg.Id)
}
