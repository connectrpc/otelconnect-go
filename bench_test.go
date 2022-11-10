package otelconnect

import (
	"context"
	"errors"
	"fmt"
	"github.com/bufbuild/connect-go"
	pingv1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1"
	"github.com/bufbuild/connect-opentelemetry-go/internal/gen/observability/ping/v1/pingv1connect"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

const concurrency = 1

func startBenchServer(lis net.Listener, options ...connect.HandlerOption) *http.Server {
	addr := lis.Addr().String()
	mux := http.NewServeMux()
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		MaxHeaderBytes:    8 * 1024, // 8KiB
	}
	mux.Handle(pingv1connect.NewPingServiceHandler(&PingServer{}, options...))
	go func() {
		if err := srv.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP listen and serve: %v", err)
		}
	}()

	mux := http.NewServeMux()
	mux.Handle(pingv1connect.NewPingServiceHandler(&PingServer{}, options...))
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	connectClient := pingv1connect.NewPingServiceClient(
		server.Client(),
		server.URL,
	)

	return srv
}

func BenchmarkUnaryOtel(b *testing.B) {
	lis, err := net.Listen("tcp", ":0")
	if err != nil {
		b.Error(err)
	}
	addr := fmt.Sprintf("http://localhost:%d", lis.Addr().(*net.TCPAddr).Port)
	svr := startBenchServer(lis, WithTelemetry(Server))
	b.Cleanup(func() {
		svr.Shutdown(context.Background())
		lis.Close()
	})
	client := pingv1connect.NewPingServiceClient(http.DefaultClient, addr, WithTelemetry(Client))

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
func BenchmarkUnary(b *testing.B) {
	lis, err := net.Listen("tcp", ":0")
	if err != nil {
		b.Error(err)
	}
	addr := fmt.Sprintf("http://localhost:%d", lis.Addr().(*net.TCPAddr).Port)
	svr := startBenchServer(lis, WithTelemetry(Server))
	b.Cleanup(func() {
		svr.Shutdown(context.Background())
		lis.Close()
	})
	client := pingv1connect.NewPingServiceClient(http.DefaultClient, addr)

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
