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

## An example

```go
package main

import (
  "context"
  "fmt"
  "log"
  "net/http"

  "github.com/bufbuild/connect-go"
  otelconnect "github.com/bufbuild/connect-opentelemetry-go"

  // Generated from your protobuf schema by protoc-gen-go and
  // protoc-gen-connect-go.
  pingv1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1"
  "github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1/pingv1connect"
)

func main() {
  mux := http.NewServeMux()

  // otelconnect.WithTelemetry adds tracing and metrics to both clients and
  // handlers. By default, it uses OpenTelemetry's global TracerProvider and
  // MeterProvider, which you can configure by following the OpenTelemetry
  // documentation. If you'd prefer to avoid globals, use
  // otelconnect.WithTracerProvider and otelconnect.WithMeterProvider.
  mux.Handle(pingv1connect.NewPingServiceHandler(
    &pingv1connect.UnimplementedPingServiceHandler{},
    otelconnect.WithTelemetry(),
  ))

  http.ListenAndServe("localhost:8080", mux)
}

func makeRequest() {
  client := pingv1connect.NewPingServiceClient(
    http.DefaultClient,
    "http://localhost:8080",
    otelconnect.WithTelemetry(),
  )
  resp, err := client.Ping(
    context.Background(),
    connect.NewRequest(&pingv1.PingRequest{}),
  )
  if err != nil {
    log.Fatal(err)
  }
  fmt.Println(resp)
}
```

## Status

`connect-opentelemetry-go` is available as an untagged alpha release. It's
missing tracing support for streaming RPCs, but otherwise supports metrics and
tracing for both clients and handlers.

|         | Unary | Streaming Client | Streaming Handler |
|---------|:-----:|:----------------:|:-----------------:|
| Metrics | ✅    | ✅               | ✅                |
| Tracing | ✅    | ❌               | ❌                |

For progress on streaming tracing, see [this
issue](https://github.com/bufbuild/connect-opentelemetry-go/issues/28).

We plan to tag a production-ready beta release, with tracing for streaming
RPCs, by the end of 2022. Users of this package should expect breaking changes
as [the underlying OpenTelemetry
APIs](https://opentelemetry.io/docs/instrumentation/go/#status-and-releases)
change. Once the Go OpenTelemetry metrics SDK stabilizes, we'll
release a stable v1 of this package.

## Support and Versioning

`connect-opentelemetry-go` supports:

* The [two most recent major releases][go-support-policy] of Go.
* v1 of the `go.opentelemetry.io/otel` tracing SDK.
* The current alpha release of the `go.opentelemetry.io/otel` metrics SDK.

It's not yet stable. We take every effort to maintain backward compatibility,
but can't commit to a stable v1 until the OpenTelemetry APIs are stable.

## Legal

Offered under the [Apache 2 license][license].

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
