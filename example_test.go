package otelconnect

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/bufbuild/connect-go"
	pingv1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1"
	"github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1/pingv1connect"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func ExampleWithTelemetry() {
	metricReader := metricsdk.NewManualReader()
	meterProvider := metricsdk.NewMeterProvider(
		metricsdk.WithReader(metricReader),
		metricsdk.WithResource(metricResource()),
	)
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	// create telemetry option that can be passed in as both client and handler options
	telemetryOption := WithTelemetry(WithMeterProvider(meterProvider), WithTracerProvider(traceProvider))
	mux := http.NewServeMux()
	pingServer := &pluggablePingServer{ping: pingHappy, cumSum: cumSumHappy}
	mux.Handle(pingv1connect.NewPingServiceHandler(pingServer, telemetryOption)) // pass in Telemetry option
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()

	pingClient := pingv1connect.NewPingServiceClient(server.Client(), server.URL, telemetryOption) // pass in Telemetry option

	// unary request
	_, err := pingClient.Ping(context.Background(), connect.NewRequest(&pingv1.PingRequest{}))
	if err != nil {
		log.Fatal(err)
	}

	// streaming request
	stream := pingClient.CumSum(context.Background())
	if err := stream.Send(&pingv1.CumSumRequest{Number: 1}); err != nil {
		log.Fatal(err)
	}
	if _, err = stream.Receive(); err != nil {
		log.Fatal(err)
	}
	if err := stream.CloseRequest(); err != nil {
		log.Fatal(err)
	}

	// spans recorded for client + handler in unary interceptor
	spans := spanRecorder.Ended()
	fmt.Println("number of spans recorded:", len(spans))

	// metrics recorded from handler + client interceptor
	metricData, err := metricReader.Collect(context.Background())
	if err != nil {
		log.Fatalln(err)
	}
	fmt.Println("number of metrics recorded:", len(metricData.ScopeMetrics[0].Metrics))

	// output:
	// number of spans recorded: 2
	// number of metrics recorded: 10
}
