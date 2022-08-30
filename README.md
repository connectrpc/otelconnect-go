connect-opentelemetry-go
========================

[![Build](https://github.com/bufbuild/connect-opentelemetry-go/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/bufbuild/connect-opentelemetry-go/actions/workflows/ci.yaml)
[![Report Card](https://goreportcard.com/badge/github.com/bufbuild/connect-opentelemetry-go)](https://goreportcard.com/report/github.com/bufbuild/connect-opentelemetry-go)
[![GoDoc](https://pkg.go.dev/badge/github.com/bufbuild/connect-opentelemetry-go.svg)](https://pkg.go.dev/github.com/bufbuild/connect-opentelemetry-go)

`connect-opentelemetry-go` adds support for [OpenTelemetry] tracing and metrics
collection to [Connect][connect-go] servers and clients.

For more on Connect, see the [announcement blog post][blog], the documentation
on [connect.build][docs] (especially the [Getting Started] guide), the
[`connect-go`][connect-go] repo, or the [demo service][demo].

For more on OpenTelemetry, see the official [Go OpenTelemetry
packages][otel-go], [opentelemetry.io], and the [Go
quickstart][otel-go-quickstart].

## A small example

### Server

```go
// TODO
```

### Client

```go
// TODO
```

## Status

Like [`connect-go`][connect-go], this module is a beta: we may make a few changes 
as we gather feedback from early adopters. We're planning to tag a stable v1 in 
October, soon after the Go 1.19 release.

## Support and Versioning

`connect-opentelemetry-go` supports:

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
