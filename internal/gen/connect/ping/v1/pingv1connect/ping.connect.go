// Code generated by protoc-gen-connect-go. DO NOT EDIT.
//
// Source: connect/ping/v1/ping.proto

package pingv1connect

import (
	context "context"
	errors "errors"
	connect_go "github.com/bufbuild/connect-go"
	v1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/connect/ping/v1"
	http "net/http"
	strings "strings"
)

// This is a compile-time assertion to ensure that this generated file and the connect package are
// compatible. If you get a compiler error that this constant is not defined, this code was
// generated with a version of connect newer than the one compiled into your binary. You can fix the
// problem by either regenerating this code with an older version of connect or updating the connect
// version compiled into your binary.
const _ = connect_go.IsAtLeastVersion0_1_0

const (
	// PingServiceName is the fully-qualified name of the PingService service.
	PingServiceName = "connect.ping.v1.PingService"
)

// PingServiceClient is a client for the connect.ping.v1.PingService service.
type PingServiceClient interface {
	Ping(context.Context, *connect_go.Request[v1.PingRequest]) (*connect_go.Response[v1.PingResponse], error)
}

// NewPingServiceClient constructs a client for the connect.ping.v1.PingService service. By default,
// it uses the Connect protocol with the binary Protobuf Codec, asks for gzipped responses, and
// sends uncompressed requests. To use the gRPC or gRPC-Web protocols, supply the connect.WithGRPC()
// or connect.WithGRPCWeb() options.
//
// The URL supplied here should be the base URL for the Connect or gRPC server (for example,
// http://api.acme.com or https://acme.com/grpc).
func NewPingServiceClient(httpClient connect_go.HTTPClient, baseURL string, opts ...connect_go.ClientOption) PingServiceClient {
	baseURL = strings.TrimRight(baseURL, "/")
	return &pingServiceClient{
		ping: connect_go.NewClient[v1.PingRequest, v1.PingResponse](
			httpClient,
			baseURL+"/connect.ping.v1.PingService/Ping",
			opts...,
		),
	}
}

// pingServiceClient implements PingServiceClient.
type pingServiceClient struct {
	ping *connect_go.Client[v1.PingRequest, v1.PingResponse]
}

// Ping calls connect.ping.v1.PingService.Ping.
func (c *pingServiceClient) Ping(ctx context.Context, req *connect_go.Request[v1.PingRequest]) (*connect_go.Response[v1.PingResponse], error) {
	return c.ping.CallUnary(ctx, req)
}

// PingServiceHandler is an implementation of the connect.ping.v1.PingService service.
type PingServiceHandler interface {
	Ping(context.Context, *connect_go.Request[v1.PingRequest]) (*connect_go.Response[v1.PingResponse], error)
}

// NewPingServiceHandler builds an HTTP handler from the service implementation. It returns the path
// on which to mount the handler and the handler itself.
//
// By default, handlers support the Connect, gRPC, and gRPC-Web protocols with the binary Protobuf
// and JSON codecs. They also support gzip compression.
func NewPingServiceHandler(svc PingServiceHandler, opts ...connect_go.HandlerOption) (string, http.Handler) {
	mux := http.NewServeMux()
	mux.Handle("/connect.ping.v1.PingService/Ping", connect_go.NewUnaryHandler(
		"/connect.ping.v1.PingService/Ping",
		svc.Ping,
		opts...,
	))
	return "/connect.ping.v1.PingService/", mux
}

// UnimplementedPingServiceHandler returns CodeUnimplemented from all methods.
type UnimplementedPingServiceHandler struct{}

func (UnimplementedPingServiceHandler) Ping(context.Context, *connect_go.Request[v1.PingRequest]) (*connect_go.Response[v1.PingResponse], error) {
	return nil, connect_go.NewError(connect_go.CodeUnimplemented, errors.New("connect.ping.v1.PingService.Ping is not implemented"))
}
