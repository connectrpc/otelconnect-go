connect-opentelemetry-go
========================

[![Build](https://github.com/bufbuild/connect-opentelemetry-go/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/bufbuild/connect-opentelemetry-go/actions/workflows/ci.yaml)
[![Report Card](https://goreportcard.com/badge/github.com/bufbuild/connect-opentelemetry-go)](https://goreportcard.com/report/github.com/bufbuild/connect-opentelemetry-go)
[![GoDoc](https://pkg.go.dev/badge/github.com/bufbuild/connect-opentelemetry-go.svg)](https://pkg.go.dev/github.com/bufbuild/connect-opentelemetry-go)



|         | Unary | Streaming Client | Streaming Handler |
|---------|-------|------------------|-------------------|
| Metrics | ✅     | ✅                | ✅                 |
| Tracing | ✅     | ❌                | ❌                 |

For progress on streaming tracing see: https://github.com/bufbuild/connect-opentelemetry-go/issues/28


`connect-opentelemetry-go` adds support for [OpenTelemetry][opentelemetry.io]
tracing and metrics collection to [Connect][connect-go] servers and clients.

For more on Connect, see the [announcement blog post][blog], the documentation
on [connect.build][docs] (especially the [Getting Started] guide), the
[`connect-go`][connect-go] repo, or the [demo service][demo].

For more on OpenTelemetry, see the official [Go OpenTelemetry
packages][otel-go], [opentelemetry.io], and the [Go
quickstart][otel-go-quickstart].

## A small example

### Server

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/bufbuild/connect-go"
	otelconnect "github.com/bufbuild/connect-opentelemetry-go"
	pingv1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1"
	"github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1/pingv1connect"
	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	promexport "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/zipkin"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const (
	prometheusTarget = "http://localhost:9091"
	zipkinTarget     = "http://localhost:9411"
	prometheusJob    = "services"
	serverAddress    = "localhost:7777"
)

func main() {
	// push metrics from global prometheus registry
	pusher := push.New(prometheusTarget, prometheusJob).Gatherer(promclient.DefaultGatherer)
	defer pusher.Push() // push all global metrics when finished
	mux := http.NewServeMux()
	mux.Handle(
		pingv1connect.NewPingServiceHandler(
			&PingServer{},
			otelconnect.WithTelemetry( // configure connect handler with telemetry
				otelconnect.WithTracerProvider(zipkinTracing()), // Set trace provider to export to zipkin
				otelconnect.WithMeterProvider(promMetrics()),    // Set meter provider to export to prometheus
			),
		),
	)
	go http.ListenAndServe(serverAddress, h2c.NewHandler(mux, &http2.Server{}))
	client := pingv1connect.NewPingServiceClient(
		http.DefaultClient,
		"http://"+serverAddress,
		otelconnect.WithTelemetry( // configure connect client with telemetry
			otelconnect.WithTracerProvider(zipkinTracing()), // Set trace provider to export to zipkin
			otelconnect.WithMeterProvider(promMetrics()),    // Set meter provider to export to prometheus
		),
	)
	resp, err := client.Ping(context.Background(), connect.NewRequest(&pingv1.PingRequest{Id: 42}))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("response:", resp)
}

// promMetrics returns a metric peter provider that exports to the global prometheus registry.
func promMetrics() *metricsdk.MeterProvider {
	exporter, err := promexport.New(
		promexport.WithRegisterer(promclient.DefaultRegisterer),
		promexport.WithoutScopeInfo(),
		promexport.WithoutTargetInfo(),
	)
	if err != nil {
		log.Fatal(err)
	}
	return metricsdk.NewMeterProvider(metricsdk.WithReader(exporter))
}

// zipkinTracing returns a trace provider that exports traces to a zipkin target.
func zipkinTracing() *sdktrace.TracerProvider {
	exporter, err := zipkin.New(zipkinTarget + "/api/v2/spans")
	if err != nil {
		log.Fatal(err)
	}
	return sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)))
}
```

## Status

Like [`connect-go`][connect-go], this module is a beta: we may make a few changes 
as we gather feedback from early adopters. We're planning to tag a stable v1 in 
October, soon after the Go 1.19 release.

## Support and Versioning

* The [two most recent major releases][go-support-policy] of Go.
* [APIv2][] of protocol buffers in Go (`google.golang.org/protobuf`).

Within those parameters, it follows semantic versioning.

## Legal

Offered under the [Apache 2 license][license].

[APIv2]: https://blog.golang.org/protobuf-apiv2
[blog]: https://buf.build/blog/connect-a-better-grpc
[connect-go]: https://github.com/bufbuild/connect-go
[demo]: https://github.com/bufbuild/connect-demo
[docs]: https://connect.build
[Getting Started]: https://connect.build/docs/go/getting-started
[go-support-policy]: https://golang.org/doc/devel/release#policy
[license]: https://github.com/bufbuild/connect-opentelemetry-go/blob/main/LICENSE
[opentelemetry.io]: https://opentelemetry.io/
[otel-go]: https://github.com/open-telemetry/opentelemetry-go
[otel-go-quickstart]: https://opentelemetry.io/docs/instrumentation/go/getting-started/
