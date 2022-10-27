connect-opentelemetry-go
========================

[![Build](https://connectrpc.com/otelconnect/actions/workflows/ci.yaml/badge.svg?branch=main)](https://connectrpc.com/otelconnect/actions/workflows/ci.yaml)
[![Report Card](https://goreportcard.com/badge/connectrpc.com/otelconnect)](https://goreportcard.com/report/connectrpc.com/otelconnect)
[![GoDoc](https://pkg.go.dev/badge/connectrpc.com/otelconnect.svg)](https://pkg.go.dev/connectrpc.com/otelconnect)

`connect-opentelemetry-go` adds support for [OpenTelemetry][opentelemetry.io]
tracing and metrics collection to [Connect][connect-go] servers and clients.

For more on Connect, see the [announcement blog post][blog], the documentation
on [connectrpc.com][docs] (especially the [Getting Started] guide), the
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
[connect-go]: https://connectrpc.com/connect
[demo]: https://github.com/bufbuild/connect-demo
[docs]: https://connectrpc.com
[Getting Started]: https://connectrpc.com/go/getting-started
[go-support-policy]: https://golang.org/doc/devel/release#policy
[license]: https://connectrpc.com/otelconnect/blob/main/LICENSE
[opentelemetry.io]: https://opentelemetry.io/
[otel-go]: https://github.com/open-telemetry/opentelemetry-go
[otel-go-quickstart]: https://opentelemetry.io/docs/instrumentation/go/getting-started/
