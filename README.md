connect-opencensus-go
=====================

[![License](https://img.shields.io/github/license/bufbuild/connect-opencensus-go?color=blue)][license]
[![CI](https://github.com/bufbuild/connect-opencensus-go/actions/workflows/ci.yaml/badge.svg?branch=main)][ci]

`connect-opencensus-go` adds support for [OpenCensus] application metrics collection on a 
Connect server or client. It provides multiple tags, stats, and views to be tracked and recorded.

`connect-opencensus-go` is built on top of the `ochttp` package of opencensus-go and is expected 
to be used together with the `ochttp`, see the examples below for details.

For more on Connect, see the [announcement blog post][blog], the documentation
on [connectrpc.com][docs] (especially the [Getting Started] guide for Go), the
[`connect-go`][connect-go] repo, or the [demo service][demo].

For more on OpenCensus, see the official [OpenCensus Libraries for Go][opencensus-go], 
the documentation on [OpenCensus Website][OpenCensus], and the [quickstart][opencensus-go-quickstart]
guide for Go.

## Example

### Server

```go
package main

import (
	"example/internal/gen/buf/connect/demo/eliza/v1/elizav1connect"
	"log"
	"net/http"

	"connectrpc.com/connect"
	occonnect "github.com/bufbuild/connect-opencensus-go"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/stats/view"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type elizaServer struct {
	elizav1connect.UnimplementedElizaServiceHandler
}

func main() {
	// register server views from occonnect, you can also register those from ochttp.
	if err := view.Register(
		occonnect.ServerLatencyView,
		occonnect.ServerCompletedRPCsView,
		occonnect.ServerSentMessagesPerRPCView,
		occonnect.ServerReceivedMessagesPerRPCView,
	); err != nil {
		log.Fatalf("Failed to register server views for connect metrics: %v", err)
	}
	// start your exporter and enable observability to extract and examine stats,
	// see: https://opencensus.io/guides/http/go/net_http/server/
	enableObservabilityAndExporters()

	// your own connect handler.
	mux := http.NewServeMux()
	mux.Handle(elizav1connect.NewElizaServiceHandler(
		&elizaServer{},
		connect.WithInterceptors(
			occonnect.NewInterceptor(),
		),
	))
	// use ochttp.Handler to wrap your original handler.
	handler := &ochttp.Handler{Handler: mux}
	// If you don't need to support HTTP/2 without TLS (h2c), you can drop
	// x/net/http2 and use http.ListenAndServeTLS instead.
	http.ListenAndServe(
		":8080",
		h2c.NewHandler(handler, &http2.Server{}),
	)
}
```

### Client

```go
package main

import (
	"context"
	elizav1 "example/internal/gen/buf/connect/demo/eliza/v1"
	"example/internal/gen/buf/connect/demo/eliza/v1/elizav1connect"
	"log"
	"net/http"

	"connectrpc.com/connect"
	occonnect "github.com/bufbuild/connect-opencensus-go"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/stats/view"
)

func main() {
	// register client views from occonnect, you can also register those from ochttp.
	if err := view.Register(
		occonnect.ClientRoundtripLatencyView,
		occonnect.ClientCompletedRPCsView,
		occonnect.ClientSentMessagesPerRPCView,
		occonnect.ClientReceivedMessagesPerRPCView,
	); err != nil {
		log.Fatalf("Failed to register client views for connect metrics: %v", err)
	}
	// start your exporter and enable observability to extract and examine stats,
	// see: https://opencensus.io/guides/http/go/net_http/client/
	enableObservabilityAndExporters()

	// use ochttp.Transport to wrap your original transport.
	transport := &ochttp.Transport{
		Base: http.DefaultTransport,
	}
	// create the client with the wrapped transport
	client := elizav1connect.NewElizaServiceClient(
		&http.Client{Transport: transport},
		"http://localhost:8080/",
	)
	// use the client.
	_ = client
}
```

## Status

Like [`connect-go`][connect-go], this module is a beta: we may make a few changes 
as we gather feedback from early adopters. We're planning to tag a stable v1 in 
October, soon after the Go 1.19 release.

## Support and Versioning

`connect-opencensus-go` supports:

* The [two most recent major releases][go-support-policy] of Go, with a minimum
  of Go 1.18.
* [APIv2][] of protocol buffers in Go (`google.golang.org/protobuf`).

Within those parameters, it follows semantic versioning.

## Legal

Offered under the [Apache 2 license][license].

[APIv2]: https://blog.golang.org/protobuf-apiv2
[blog]: https://buf.build/blog/connect-a-better-grpc
[ci]: https://github.com/bufbuild/connect-opencensus-go/actions/workflows/ci.yaml
[connect-go]: https://connectrpc.com/connect
[demo]: https://github.com/bufbuild/connect-demo
[docs]: https://connectrpc.com
[Getting Started]: https://connectrpc.com/go/getting-started
[go-support-policy]: https://golang.org/doc/devel/release#policy
[license]: https://github.com/bufbuild/connect-opencensus-go/blob/main/LICENSE
[OpenCensus]: https://opencensus.io/
[opencensus-go]: https://github.com/census-instrumentation/opencensus-go
[opencensus-go-quickstart]: https://opencensus.io/quickstart/go/
