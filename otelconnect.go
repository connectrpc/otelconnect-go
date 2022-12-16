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

// Package otelconnect provides OpenTelemetry tracing and metrics for
// [github.com/bufbuild/connect-go] servers and clients.
package otelconnect

import (
	"errors"
	"fmt"
	"go.opentelemetry.io/otel/codes"
	"google.golang.org/protobuf/proto"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	time "time"

	"github.com/bufbuild/connect-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/global"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
)

const (
	version             = "0.0.1-dev"
	semanticVersion     = "semver:" + version
	instrumentationName = "github.com/bufbuild/connect-opentelemetry-go"
)

// WithTelemetry constructs a connect.Option that adds OpenTelemetry metrics
// and tracing to Connect clients and handlers.
func WithTelemetry(options ...Option) connect.Option {
	return connect.WithInterceptors(NewInterceptor(options...))
}

// NewInterceptor constructs and returns OpenTelemetry Interceptors for metrics
// and tracing.
func NewInterceptor(options ...Option) connect.Interceptor {
	cfg := config{
		now:            time.Now,
		tracerProvider: otel.GetTracerProvider(),
		propagator:     otel.GetTextMapPropagator(),
		meter: global.MeterProvider().Meter(
			instrumentationName,
			metric.WithInstrumentationVersion(semanticVersion),
		),
	}
	for _, opt := range options {
		opt.apply(&cfg)
	}
	intercept := newInterceptor(cfg)
	return intercept
}

// Request is the information about each RPC available to filter functions. It
// contains the common subset of [connect.AnyRequest],
// [connect.StreamingClientConn], and [connect.StreamingHandlerConn].
type Request struct {
	Spec   connect.Spec
	Peer   connect.Peer
	Header http.Header
}

func protocolToSemConv(peer string) string {
	switch peer {
	case "grpcweb":
		return "grpc_web"
	case "grpc":
		return "grpc"
	case "connect":
		return "buf_connect"
	default:
		return peer
	}
}

func procedureAttributes(procedure string) []attribute.KeyValue {
	parts := strings.SplitN(procedure, "/", 2)
	var attrs []attribute.KeyValue
	switch len(parts) {
	case 0:
		return attrs // invalid
	case 1:
		// fall back to treating the whole string as the method
		if method := parts[0]; method != "" {
			attrs = append(attrs, semconv.RPCMethodKey.String(method))
		}
	default:
		if svc := parts[0]; svc != "" {
			attrs = append(attrs, semconv.RPCServiceKey.String(svc))
		}
		if method := parts[1]; method != "" {
			attrs = append(attrs, semconv.RPCMethodKey.String(method))
		}
	}
	return attrs
}

func spanStatus(err error) (codes.Code, string) {
	if err == nil {
		return codes.Ok, ""
	}
	if connectErr := new(connect.Error); errors.As(err, &connectErr) {
		return codes.Error, connectErr.Message()
	}
	return codes.Error, err.Error()
}

func eventAttributes(msg any, counter int, attributes ...attribute.KeyValue) []attribute.KeyValue {
	var size int
	if msg, ok := msg.(proto.Message); ok {
		size = proto.Size(msg)
		attributes = append(attributes, semconv.MessageUncompressedSizeKey.Int(size))
	}
	return append(attributes, semconv.MessageIDKey.Int(counter))
}

func requestAttributes(req *Request) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if addr := req.Peer.Addr; addr != "" {
		attrs = append(attrs, addressAttributes(addr)...)
	}
	name := strings.TrimLeft(req.Spec.Procedure, "/")
	protocol := protocolToSemConv(req.Peer.Protocol)
	attrs = append(attrs, semconv.RPCSystemKey.String(protocol))
	attrs = append(attrs, procedureAttributes(name)...)
	return attrs
}

func statusCodeAttribute(protocol string, serverErr error) attribute.KeyValue {
	codeKey := attribute.Key("rpc." + protocol + ".status_code")
	// Following the respective specifications, use integers for gRPC codes and
	// strings for Connect codes.
	if strings.HasPrefix(protocol, "grpc") {
		if serverErr != nil {
			return codeKey.Int64(int64(connect.CodeOf(serverErr)))
		}
		return codeKey.Int64(0) // gRPC uses 0 for success
	} else if serverErr != nil {
		return codeKey.String(connect.CodeOf(serverErr).String())
	}
	return codeKey.String("success")
}

func addressAttributes(address string) []attribute.KeyValue {
	if addrPort, err := netip.ParseAddrPort(address); err == nil {
		return []attribute.KeyValue{
			semconv.NetPeerIPKey.String(addrPort.Addr().String()),
			semconv.NetPeerPortKey.Int(int(addrPort.Port())),
		}
	}
	if host, port, err := net.SplitHostPort(address); err == nil {
		portint, err := strconv.Atoi(port)
		if err != nil {
			return []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(portint),
			}
		}
	}
	return []attribute.KeyValue{semconv.NetPeerNameKey.String(address)}
}

func formatkeys(interceptorType string, metricName string) string {
	return fmt.Sprintf(metricKeyFormat, interceptorType, metricName)
}
