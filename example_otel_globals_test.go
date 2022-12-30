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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric/global"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
)

// Example_serverGlobals shows how global OpenTelemetry variables can be set
// in order to use [WithTelemetry] without any further configuration in handler constructors.
func Example_serverGlobals() {
	// Set otel globals.
	otel.SetTracerProvider(trace.NewTracerProvider())
	global.SetMeterProvider(metric.NewMeterProvider())
	otel.SetTextMapPropagator(propagation.TraceContext{})
	// Construct handler with otelconnect.WithTelemetry
	mux := http.NewServeMux()
	mux.Handle(
		pingv1connect.NewPingServiceHandler(
			&pingv1connect.UnimplementedPingServiceHandler{}, // Or use custom implementation here
			otelconnect.WithTelemetry(),                      // Use without any extra configuration
		),
	)
	log.Fatal(http.ListenAndServe(":8080", mux))
}

// Example_clientGlobals shows how global OpenTelemetry variables can be set
// in order to use [WithTelemetry] without any further configuration in client constructors.
func Example_clientGlobals() {
	// Set otel globals.
	otel.SetTracerProvider(trace.NewTracerProvider())
	global.SetMeterProvider(metric.NewMeterProvider())
	otel.SetTextMapPropagator(propagation.TraceContext{})
	client := pingv1connect.NewPingServiceClient(
		http.DefaultClient,
		"http://localhost:8080",
		otelconnect.WithTelemetry(), // Use without any extra configuration
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
