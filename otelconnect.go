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
	"net/http"
	"strings"

	"github.com/bufbuild/connect-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

const (
	version             = "0.0.1-dev"
	semanticVersion     = "semver:" + version
	instrumentationName = "github.com/bufbuild/connect-opentelemetry-go"
)

// WithTelemetry constructs a connect.Option that adds OpenTelemetry metrics
// and tracing to Connect clients and handlers.
func WithTelemetry(options ...Option) connect.Option {
	cfg := config{
		Trace: traceConfig{
			Provider:   otel.GetTracerProvider(),
			Propagator: otel.GetTextMapPropagator(),
		},
		Metrics: metricsConfig{},
	}
	for _, opt := range options {
		opt.apply(&cfg)
	}
	var interceptors []connect.Interceptor
	if !cfg.DisableMetrics {
		interceptors = append(interceptors, &metricsInterceptor{cfg.Metrics})
	}
	if !cfg.DisableTrace {
		interceptors = append(interceptors, &traceInterceptor{cfg.Trace})
	}
	return connect.WithInterceptors(interceptors...)
}

// Request is the information about each RPC available to filter functions. It
// contains the common subset of [connect.AnyRequest],
// [connect.StreamingClientConn], and [connect.StreamingHandlerConn].
type Request struct {
	Spec   connect.Spec
	Peer   connect.Peer
	Header http.Header
}

type config struct {
	DisableTrace   bool
	Trace          traceConfig
	DisableMetrics bool
	Metrics        metricsConfig
}

func parseProtocol(header http.Header) string {
	ctype := header.Get("Content-Type")
	if strings.HasPrefix(ctype, "application/grpc-web") {
		return "grpc_web"
	}
	if strings.HasPrefix(ctype, "application/grpc") {
		return "grpc"
	}
	return "connect"
}

func parseProcedure(procedure string) []attribute.KeyValue {
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
