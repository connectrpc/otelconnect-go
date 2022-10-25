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
	"github.com/bufbuild/connect-go"
	pingv1 "github.com/bufbuild/connect-opentelemetry-go/internal/gen/connect/ping/v1"
	"github.com/bufbuild/connect-opentelemetry-go/internal/gen/connect/ping/v1/pingv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Semantic conventions for attribute keys for gRPC.
const (
	// Type of message transmitted or received.
	RPCMessageTypeKey = attribute.Key("message.type")

	// Identifier of message transmitted or received.
	RPCMessageIDKey = attribute.Key("message.id")

	// The uncompressed size of the message transmitted or received in
	// bytes.
	RPCMessageUncompressedSizeKey = attribute.Key("message.uncompressed_size")
)

func doCalls(req *connect.Request[pingv1.PingRequest], handlerOption ...connect.HandlerOption) (*connect.Response[pingv1.PingResponse], error) {
	mux := http.NewServeMux()
	mux.Handle(pingv1connect.NewPingServiceHandler(&PingServer{}, handlerOption...))
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	httpclient := server.Client()
	client := pingv1connect.NewPingServiceClient(
		httpclient,
		server.URL,
	)
	return client.Ping(context.Background(), req)
}

func randombytes(size int) string {
	body := make([]byte, size)
	return string(body)
}

func TestInterceptors(t *testing.T) {
	clientUnarySR := tracetest.NewSpanRecorder()
	clientUnaryTP := trace.NewTracerProvider(trace.WithSpanProcessor(clientUnarySR))
	_, err := doCalls(connect.NewRequest(&pingv1.PingRequest{Number: 42}), WithTelemetry(WithTracerProvider(clientUnaryTP)))
	if err != nil {
		t.Errorf(err.Error())
	}

	_, err = doCalls(connect.NewRequest(&pingv1.PingRequest{Text: randombytes(10)}), WithTelemetry(WithTracerProvider(clientUnaryTP)))
	if err != nil {
		t.Errorf(err.Error())
	}

	checkUnaryClientSpans(t, clientUnarySR.Ended(), []asserts{
		{
			spanName: "connect.ping.v1.PingService/Ping",
			events: []trace.Event{
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						RPCMessageTypeKey.String("RECEIVED"),
						RPCMessageIDKey.Int(1),
						RPCMessageUncompressedSizeKey.Int(2),
					},
				},
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						RPCMessageTypeKey.String("SENT"),
						RPCMessageIDKey.Int(1),
						RPCMessageUncompressedSizeKey.Int(2),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("connect.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
				semconv.NetPeerNameKey.String("127.0.0.1"),
				semconv.NetPeerPortKey.String(""),
				attribute.Key("rpc.connect.status_code").String("success"),
			},
		},
		{
			spanName: "connect.ping.v1.PingService/Ping",
			events: []trace.Event{
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						RPCMessageTypeKey.String("RECEIVED"),
						RPCMessageIDKey.Int(1),
						RPCMessageUncompressedSizeKey.Int(12),
					},
				},
				{
					Name: "message",
					Attributes: []attribute.KeyValue{
						RPCMessageTypeKey.String("SENT"),
						RPCMessageIDKey.Int(1),
						RPCMessageUncompressedSizeKey.Int(0),
					},
				},
			},
			attrs: []attribute.KeyValue{
				semconv.RPCSystemKey.String("connect"),
				semconv.RPCServiceKey.String("connect.ping.v1.PingService"),
				semconv.RPCMethodKey.String("Ping"),
				semconv.NetPeerNameKey.String("127.0.0.1"),
				semconv.NetPeerPortKey.String(""),
				attribute.Key("rpc.connect.status_code").String("success"),
			},
		},
	})

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
		Number: req.Msg.Number,
	})
	res.Header().Set("Some-Other-Header", "hello!")
	return res, nil
}

type PingServer struct {
	pingv1connect.UnimplementedPingServiceHandler // returns errors from all methods
}

type asserts struct {
	spanName string
	events   []trace.Event
	attrs    []attribute.KeyValue
}

func checkUnaryClientSpans(t *testing.T, spans []trace.ReadOnlySpan, want []asserts) {
	comparer := cmp.Comparer(func(x, y attribute.KeyValue) bool {
		return x.Value == x.Value && y.Key == y.Key
	})
	for i, span := range spans {
		wantEvents := want[i].events
		wantAttributes := want[i].attrs
		if span.EndTime().IsZero() {
			t.Fail()
		}
		if span.Name() != want[i].spanName {
			t.Errorf("span name not %s", want[i].spanName)
		}
		gotEvents := span.Events()
		if len(wantEvents) != len(gotEvents) {
			t.Error("event lengths do not match")
		}
		for i, e := range wantEvents {
			if e.Name != gotEvents[i].Name {
				t.Error("names do not match")
			}

			diff := cmp.Diff(e.Attributes, gotEvents[i].Attributes, comparer)
			if diff != "" {
				t.Error(diff)
			}
		}
		diff := cmp.Diff(span.Attributes(), wantAttributes,
			cmpopts.IgnoreUnexported(attribute.Value{}),
			cmp.Comparer(func(x, y attribute.KeyValue) bool {
				if x.Key == semconv.NetPeerPortKey && y.Key == semconv.NetPeerPortKey {
					return true
				}
				return x.Key == y.Key && x.Value == y.Value
			},
			))
		if diff != "" {
			t.Error(diff)
		}
	}
}
