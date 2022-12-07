connect-opentelemetry-go
========================

[![Build](https://github.com/bufbuild/connect-opentelemetry-go/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/bufbuild/connect-opentelemetry-go/actions/workflows/ci.yaml)
[![Report Card](https://goreportcard.com/badge/github.com/bufbuild/connect-opentelemetry-go)](https://goreportcard.com/report/github.com/bufbuild/connect-opentelemetry-go)
[![GoDoc](https://pkg.go.dev/badge/github.com/bufbuild/connect-opentelemetry-go.svg)](https://pkg.go.dev/github.com/bufbuild/connect-opentelemetry-go)

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
	"errors"
	"fmt"
	"github.com/bufbuild/connect-go"
	otelconnect "github.com/bufbuild/connect-opentelemetry-go"
	pingv1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1"
	"github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1/pingv1connect"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"io"
	"log"
	"net/http"
)

type PingServer struct {
	pingv1connect.UnimplementedPingServiceHandler // returns errors from all methods
}

func (ps *PingServer) Ping(
	ctx context.Context,
	req *connect.Request[pingv1.PingRequest],
) (*connect.Response[pingv1.PingResponse], error) {
	// connect.Request and connect.Response give you direct access to headers and
	// trailers. No context-based nonsense!
	log.Println(req.Header().Get("Some-Header"))
	res := connect.NewResponse(&pingv1.PingResponse{
		// req.Msg is a strongly-typed *pingv1.PingRequest, so we can access its
		// fields without type assertions.
		Id: req.Msg.Id,
	})
	res.Header().Set("Some-Other-Header", "hello!")
	return res, nil
}

func (ps *PingServer) CumSum(
	ctx context.Context,
	stream *connect.BidiStream[pingv1.CumSumRequest, pingv1.CumSumResponse],
) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		request, err := stream.Receive()
		if err != nil && errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return fmt.Errorf("receive request: %w", err)
		}
		if err := stream.Send(&pingv1.CumSumResponse{Sum: request.Number}); err != nil {
			return fmt.Errorf("send response: %w", err)
		}
	}
}

func main() {
	metricReader := metricsdk.NewManualReader()
	meterProvider := metricsdk.NewMeterProvider(
		metricsdk.WithReader(metricReader),
	)
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	// create telemetry option that can be passed in as both client and handler options
	telemetryOption := otelconnect.WithTelemetry(otelconnect.WithMeterProvider(meterProvider), otelconnect.WithTracerProvider(traceProvider))
	mux := http.NewServeMux()
	pingServer := &PingServer{}
	mux.Handle(pingv1connect.NewPingServiceHandler(pingServer, telemetryOption)) // pass in Telemetry option
	err := http.ListenAndServe(
		"localhost:8080",
		// For gRPC clients, it's convenient to support HTTP/2 without TLS. You can
		// avoid x/net/http2 by using http.ListenAndServeTLS.
		h2c.NewHandler(mux, &http2.Server{}),
	)
	log.Fatalf("listen failed: %v", err)
}
```

### Client

```go
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/bufbuild/connect-go"
	otelconnect "github.com/bufbuild/connect-opentelemetry-go"
	pingv1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1"
	"github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1/pingv1connect"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"golang.org/x/net/http2"
	"log"
	"net"
	"net/http"
)

func main() {
	metricReader := metricsdk.NewManualReader()
	meterProvider := metricsdk.NewMeterProvider(
		metricsdk.WithReader(metricReader),
	)
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	httpClient := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(_ context.Context, netw, addr string, cfg *tls.Config) (net.Conn, error) {
				return net.Dial(netw, addr)
			},
		},
	}
	client := pingv1connect.NewPingServiceClient(
		httpClient,
		"http://localhost:8080/",
		otelconnect.WithTelemetry(otelconnect.WithTracerProvider(traceProvider), otelconnect.WithMeterProvider(meterProvider)),
	)
	req := connect.NewRequest(&pingv1.PingRequest{
		Id: 42,
	})
	// unary request
	_, err := client.Ping(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}

	// streaming request
	stream := client.CumSum(context.Background())
	if err := stream.Send(&pingv1.CumSumRequest{Number: 1}); err != nil {
		log.Fatal(err)
	}
	if err := stream.Send(&pingv1.CumSumRequest{Number: 1}); err != nil {
		log.Fatal(err)
	}
	if _, err = stream.Receive(); err != nil {
		log.Fatal(err)
	}
	if err := stream.CloseRequest(); err != nil {
		log.Fatal(err)
	}
	// spans recorded
	spans := spanRecorder.Ended()
	fmt.Println("number of spans recorded:", len(spans))

	// metrics recorded
	metricData, err := metricReader.Collect(context.Background())
	if err != nil {
		log.Fatalln(err)
	}
	fmt.Println("number of metrics recorded:", len(metricData.ScopeMetrics[0].Metrics))

}
```

## Status

Like [`connect-go`][connect-go], this module is a beta: we may make a few changes 
as we gather feedback from early adopters. We're planning to tag a stable v1 in 
October, soon after the Go 1.19 release.

Here is what is supported for connect-opentelemetry


## Support and Versioning

`connect-opentelemetry-go` supports:

|         | Unary | Streaming Client | Streaming Handler |
|---------|-------|------------------|-------------------|
| Metrics | ✅     | ✅                | ✅                 |
| Tracing | ✅     | ❌                | ❌                 |

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
[Getting Started]: https://connect.build/go/getting-started
[go-support-policy]: https://golang.org/doc/devel/release#policy
[license]: https://github.com/bufbuild/connect-opentelemetry-go/blob/main/LICENSE
[opentelemetry.io]: https://opentelemetry.io/
[otel-go]: https://github.com/open-telemetry/opentelemetry-go
[otel-go-quickstart]: https://opentelemetry.io/docs/instrumentation/go/getting-started/
