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
	"net/http/httptest"

	"github.com/bufbuild/connect-go"
	otelconnect "github.com/bufbuild/connect-opentelemetry-go"
	pingv1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1"
	"github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1/pingv1connect"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric/global"
	"go.opentelemetry.io/otel/propagation"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// ExampleOtelGlobals shows how to set up opentelemetry tracing and metrics.
func ExampleWithTelemetry() {
	// Set the global trace providers.
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	otel.SetTracerProvider(traceProvider)
	// Set the global TextMapPropagator so the TraceID will be propagated from server to client
	tracePropagator := propagation.TraceContext{}
	otel.SetTextMapPropagator(tracePropagator)

	// Set the global meter providers.
	metricReader := metricsdk.NewManualReader()
	meterProvider := metricsdk.NewMeterProvider(
		metricsdk.WithReader(metricReader),
	)
	global.SetMeterProvider(meterProvider)
	// Start up a new test server.
	mux := http.NewServeMux()
	mux.Handle(pingv1connect.NewPingServiceHandler(&PingServer{}, otelconnect.WithTelemetry())) // Enable opentelemetry with otelconnect.WithTelemetry()
	server := httptest.NewServer(mux)
	pingClient := pingv1connect.NewPingServiceClient(server.Client(), server.URL, otelconnect.WithTelemetry()) // Enable opentelemetry with otelconnect.WithTelemetry()
	// ping the server
	_, err := pingClient.Ping(context.Background(), connect.NewRequest(&pingv1.PingRequest{}))
	if err != nil {
		log.Fatal(err)
	}
	// One span for the client, one span for the server.
	fmt.Println("number of spans: ", len(spanRecorder.Ended()))
	fmt.Println("TraceIDs equal:", spanRecorder.Ended()[0].SpanContext().TraceID() == spanRecorder.Ended()[1].SpanContext().TraceID())
	// collect metric data
	metrics, err := metricReader.Collect(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Metrics collected:", len(metrics.ScopeMetrics[0].Metrics))
	// Use telemetry with specified providers instead of otel globals
	mux = http.NewServeMux()
	mux.Handle(pingv1connect.NewPingServiceHandler(&PingServer{},
		otelconnect.WithTelemetry(
			otelconnect.WithPropagator(tracePropagator),
			otelconnect.WithTracerProvider(traceProvider),
			otelconnect.WithMeterProvider(meterProvider),
		),
	),
	)
	server = httptest.NewServer(mux)
	pingClient = pingv1connect.NewPingServiceClient(server.Client(), server.URL)
	// ping the server
	_, err = pingClient.Ping(context.Background(), connect.NewRequest(&pingv1.PingRequest{}))
	if err != nil {
		log.Fatal(err)
	}

	// output:
	// number of spans:  2
	// TraceIDs equal: true
	// Metrics collected: 10
}

type PingServer struct {
	pingv1connect.UnimplementedPingServiceHandler
}

func (p *PingServer) Ping(ctx context.Context, request *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
	return connect.NewResponse(&pingv1.PingResponse{Id: request.Msg.Id}), nil
}
