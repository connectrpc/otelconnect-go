// Copyright 2022-2025 The Connect Authors
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

package otelconnect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect/internal/gen/connectv1/observability/ping/v1/pingv1connect"
	pingv1 "connectrpc.com/otelconnect/internal/gen/observability/ping/v1"
)

func BenchmarkStreamingBaseV1(b *testing.B) {
	benchStreamingV1(b, nil, nil)
}

func BenchmarkStreamingWithInterceptorV1(b *testing.B) {
	interceptor, err := NewInterceptor()
	if err != nil {
		b.Fatal(err)
	}
	benchStreamingV1(b,
		[]connect.HandlerOption{connect.WithInterceptors(interceptor)},
		[]connect.ClientOption{connect.WithInterceptors(interceptor)},
	)
}

func BenchmarkUnaryBaseV1(b *testing.B) {
	benchUnaryV1(b, nil, nil)
}

func BenchmarkUnaryWithInterceptorV1(b *testing.B) {
	interceptor, err := NewInterceptor()
	if err != nil {
		b.Fatal(err)
	}
	benchUnaryV1(b,
		[]connect.HandlerOption{connect.WithInterceptors(interceptor)},
		[]connect.ClientOption{connect.WithInterceptors(interceptor)},
	)
}

func benchUnaryV1(b *testing.B, handleropts []connect.HandlerOption, clientopts []connect.ClientOption) {
	b.Helper()
	svr, client := startBenchServerV1(handleropts, clientopts)
	b.Cleanup(svr.Close)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		for pb.Next() {
			_, err := client.Ping(ctx, &connect.Request[pingv1.PingRequest]{
				Msg: &pingv1.PingRequest{Data: []byte("Hello, otel!")},
			})
			if err != nil {
				b.Log(err)
			}
		}
	})
}

func benchStreamingV1(b *testing.B, handleropts []connect.HandlerOption, clientopts []connect.ClientOption) {
	b.Helper()
	_, client := startBenchServerV1(handleropts, clientopts)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		for pb.Next() {
			stream := client.PingStream(ctx)
			if err := stream.Send(
				&pingv1.PingStreamRequest{
					Data: []byte("Hello, otel!"),
				}); err != nil {
				b.Error(err)
			}
			if err := stream.CloseRequest(); err != nil {
				b.Error(err)
			}
			if _, err := stream.Receive(); err != nil {
				b.Error(err)
			}
			if err := stream.CloseResponse(); err != nil {
				b.Error(err)
			}
		}
	})
}

func startBenchServerV1(handleropts []connect.HandlerOption, clientopts []connect.ClientOption) (*httptest.Server, pingv1connect.PingServiceClient) {
	mux := http.NewServeMux()
	mux.Handle(pingv1connect.NewPingServiceHandler(okayPingServerV1(), handleropts...))
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	connectClient := pingv1connect.NewPingServiceClient(
		server.Client(),
		server.URL,
		clientopts...,
	)
	return server, connectClient
}
