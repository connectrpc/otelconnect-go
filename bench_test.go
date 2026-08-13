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

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	pingv1 "connectrpc.com/otelconnect/v2/internal/gen/observability/ping/v1"
	"connectrpc.com/otelconnect/v2/internal/gen/observability/ping/v1/pingv1connect"
)

func BenchmarkStreamingBase(b *testing.B) {
	benchStreaming(b, nil, nil)
}

func BenchmarkStreamingWithInterceptor(b *testing.B) {
	serverInterceptor, err := NewServerInterceptor()
	if err != nil {
		b.Fatal(err)
	}
	clientInterceptor, err := NewClientInterceptor()
	if err != nil {
		b.Fatal(err)
	}
	benchStreaming(b,
		[]connect.ServerInterceptor{serverInterceptor},
		[]connect.ClientInterceptor{clientInterceptor},
	)
}

func BenchmarkUnaryBase(b *testing.B) {
	benchUnary(b, nil, nil)
}

func BenchmarkUnaryWithInterceptor(b *testing.B) {
	serverInterceptor, err := NewServerInterceptor()
	if err != nil {
		b.Fatal(err)
	}
	clientInterceptor, err := NewClientInterceptor()
	if err != nil {
		b.Fatal(err)
	}
	benchUnary(b,
		[]connect.ServerInterceptor{serverInterceptor},
		[]connect.ClientInterceptor{clientInterceptor},
	)
}

func benchUnary(b *testing.B, serverInterceptors []connect.ServerInterceptor, clientInterceptors []connect.ClientInterceptor) {
	b.Helper()
	svr, client := startBenchServer(serverInterceptors, clientInterceptors)
	b.Cleanup(svr.Close)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		for pb.Next() {
			_, err := client.Ping(ctx, &pingv1.PingRequest{Data: []byte("Hello, otel!")})
			if err != nil {
				b.Log(err)
			}
		}
	})
}

func benchStreaming(b *testing.B, serverInterceptors []connect.ServerInterceptor, clientInterceptors []connect.ClientInterceptor) {
	b.Helper()
	_, client := startBenchServer(serverInterceptors, clientInterceptors)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		for pb.Next() {
			stream, err := client.PingStream(ctx)
			if err != nil {
				b.Error(err)
				continue
			}
			if err := stream.Send(
				&pingv1.PingStreamRequest{
					Data: []byte("Hello, otel!"),
				}); err != nil {
				b.Error(err)
			}
			if err := stream.CloseSend(); err != nil {
				b.Error(err)
			}
			if _, err := stream.Receive(); err != nil {
				b.Error(err)
			}
			if err := stream.Close(); err != nil {
				b.Error(err)
			}
		}
	})
}

func startBenchServer(serverInterceptors []connect.ServerInterceptor, clientInterceptors []connect.ClientInterceptor) (*httptest.Server, pingv1connect.PingServiceClient) {
	mux := http.NewServeMux()
	v2server := connect.NewServer(serverInterceptors...)
	pingv1connect.RegisterPingServiceHandler(v2server, okayPingServer())
	connecthttp.Mount(mux, v2server)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	transport := connecthttp.NewTransport(server.Client(), server.URL)
	client := connect.NewClient(transport, clientInterceptors...)
	return server, pingv1connect.NewPingServiceClient(client)
}
