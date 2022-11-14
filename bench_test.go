// Copyright 2022 Buf Technologies, Inc.
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
	"sync"
	"testing"

	"github.com/bufbuild/connect-go"
	pingv1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1"
	"github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1/pingv1connect"
)

const (
	concurrency    = 5
	messagesToSend = 10
)

func BenchmarkStreamingServerNoOptions(b *testing.B) {
	testStreaming(b, nil, nil)
}

func BenchmarkStreamingServerClientOption(b *testing.B) {
	testStreaming(b, []connect.HandlerOption{WithTelemetry()}, []connect.ClientOption{WithTelemetry()})
}

func BenchmarkStreamingServerOption(b *testing.B) {
	testStreaming(b, []connect.HandlerOption{WithTelemetry()}, []connect.ClientOption{})
}

func BenchmarkStreamingClientOption(b *testing.B) {
	testStreaming(b, []connect.HandlerOption{}, []connect.ClientOption{WithTelemetry()})
}

func BenchmarkUnaryOtel(b *testing.B) {
	benchUnary(b, []connect.HandlerOption{WithTelemetry()}, []connect.ClientOption{WithTelemetry()})
}

func BenchmarkUnary(b *testing.B) {
	benchUnary(b, nil, nil)
}

func benchUnary(b *testing.B, handleropts []connect.HandlerOption, clientopts []connect.ClientOption) {
	b.Helper()
	svr, client := startBenchServer(handleropts, clientopts)
	b.Cleanup(svr.Close)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		for j := 0; j < concurrency; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := client.Ping(context.Background(), &connect.Request[pingv1.PingRequest]{
					Msg: &pingv1.PingRequest{Data: []byte("Sentence")},
				})
				if err != nil {
					b.Log(err)
				}
			}()
		}
		wg.Wait()
	}
}

func testStreaming(b *testing.B, handleropts []connect.HandlerOption, clientopts []connect.ClientOption) {
	b.Helper()
	_, client := startBenchServer(handleropts, clientopts)
	req := &pingv1.CumSumRequest{Number: 12}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		for j := 0; j < concurrency; j++ {
			stream := client.CumSum(context.Background())
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < messagesToSend; j++ {
					err := stream.Send(req)
					if err != nil {
						b.Error(err)
					}
				}
				for j := 0; j < messagesToSend; j++ {
					_, err := stream.Receive()
					if err != nil {
						b.Error(err)
					}
				}
			}()
		}
		wg.Wait()
	}
}

func startBenchServer(handleropts []connect.HandlerOption, clientopts []connect.ClientOption) (*httptest.Server, pingv1connect.PingServiceClient) {
	mux := http.NewServeMux()
	mux.Handle(pingv1connect.NewPingServiceHandler(&PingServer{}, handleropts...))
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
