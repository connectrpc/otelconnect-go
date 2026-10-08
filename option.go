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

	connectv1 "connectrpc.com/connect"
	"connectrpc.com/connect/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

var (
	// ConnectRPCSystem indicates that the semantic conventions for
	// ConnectRPC should be used.
	ConnectRPCSystem = connectRPCSystem{} //nolint:gochecknoglobals
	// GRPCSystem indicates that the semantic conventions for gRPC
	// should be used.
	GRPCSystem = gRPCSystem{} //nolint:gochecknoglobals
)

// An Option configures the OpenTelemetry instrumentation.
type Option interface {
	apply(*config)
}

// WithPropagator configures the instrumentation to use the supplied propagator
// when extracting and injecting trace context. By default, the instrumentation
// uses [otel.GetTextMapPropagator].
func WithPropagator(propagator propagation.TextMapPropagator) Option {
	return &propagatorOption{propagator}
}

// WithMeterProvider configures the instrumentation to use the supplied [metric.MeterProvider]
// when extracting and injecting trace context. By default, the instrumentation
// uses [otel.GetMeterProvider].
func WithMeterProvider(provider metric.MeterProvider) Option {
	return &meterProviderOption{provider: provider}
}

// WithTracerProvider configures the instrumentation to use the supplied
// provider when creating a tracer. By default, the instrumentation
// uses [otel.GetTracerProvider].
func WithTracerProvider(provider trace.TracerProvider) Option {
	return &tracerProviderOption{provider}
}

// WithFilter configures the instrumentation to emit traces and metrics only
// when the filter function returns true. Filter functions must be safe to call concurrently.
//
// The filter may take a connect-go v1 or v2 spec, and works with interceptors
// for either version. A v1 spec converted from a v2 RPC sets IsClient from
// the interceptor's side.
func WithFilter[F func(context.Context, connectv1.Spec) bool | func(context.Context, connect.Spec) bool](
	filter F,
) Option {
	switch filter := any(filter).(type) {
	case func(context.Context, connectv1.Spec) bool:
		return &filterOption{filterV1: filter}
	case func(context.Context, connect.Spec) bool:
		return &filterOption{filterV2: filter}
	}
	return &filterOption{}
}

// WithoutTracing disables tracing.
func WithoutTracing() Option {
	return WithTracerProvider(tracenoop.NewTracerProvider())
}

// WithoutMetrics disables metrics.
func WithoutMetrics() Option {
	return WithMeterProvider(metricnoop.NewMeterProvider())
}

// WithAttributeFilter sets the attribute filter for all metrics and trace attributes.
//
// The filter may take a connect-go v1 or v2 spec, and works with interceptors
// for either version. A v1 spec converted from a v2 RPC sets IsClient from
// the interceptor's side.
func WithAttributeFilter[F AttributeFilter | func(connectv1.Spec, attribute.KeyValue) bool | func(connect.Spec, attribute.KeyValue) bool](
	filter F,
) Option {
	switch filter := any(filter).(type) {
	case AttributeFilter:
		return &attributeFilterOption{filterV1: filter}
	case func(connectv1.Spec, attribute.KeyValue) bool:
		return &attributeFilterOption{filterV1: filter}
	case func(connect.Spec, attribute.KeyValue) bool:
		return &attributeFilterOption{filterV2: filter}
	}
	return &attributeFilterOption{}
}

// WithServerPeerAttributes adds the network.peer.address and network.peer.port
// attributes to server spans. Omitted by default: they are high-cardinality.
func WithServerPeerAttributes() Option {
	return &serverPeerAttributesOption{}
}

// WithTrustRemote sets the Interceptor to trust remote spans.
// By default, all incoming server spans are untrusted and will be linked
// with a [trace.Link] and will not be a child span.
// By default, all client spans are trusted and no change occurs when WithTrustRemote is used.
func WithTrustRemote() Option {
	return &trustRemoteOption{}
}

// WithTraceRequestHeader enables header attributes for the request header keys provided.
// Attributes will be added as Trace attributes only.
func WithTraceRequestHeader(keys ...string) Option {
	return &traceRequestHeaderOption{
		keys: keys,
	}
}

// WithTraceResponseHeader enables header attributes for the response header keys provided.
// Attributes will be added as Trace attributes only.
func WithTraceResponseHeader(keys ...string) Option {
	return &traceResponseHeaderOption{
		keys: keys,
	}
}

// WithDurationHistogramOptions passes options to the call duration
// histogram, for example [metric.WithExplicitBucketBoundaries] to replace the
// buckets recommended by the OpenTelemetry semantic conventions.
func WithDurationHistogramOptions(options ...metric.Float64HistogramOption) Option {
	return &durationHistogramOptionsOption{options: options}
}

// WithPropagateResponseHeader enables injecting the traceparent header
// into response headers for server-side interceptors. This allows clients
// to correlate their requests with server-side traces.
func WithPropagateResponseHeader() Option {
	return &propagateResponseHeaderOption{}
}

// WithRPCSystem forces the rpc.system.name attribute for the given
// RPC system. By default, the value varies based on the actual
// protocol of a request: so requests that a client sends or a server
// receives that use the gRPC or gRPC-Web protocols report "grpc";
// requests that use ConnectRPC report "connectrpc".
//
// In a system where a server handles requests for the same service but
// from clients that use multiple protocols, this causes the telemetry
// data to be partitioned by the RPC system conventions. But it is often
// desirable to instead emit uniform telemetry, to allow optics and
// aggregation across RPC systems.
//
// So this option can be used to force uniform metrics and spans, using
// the given RPC system conventions, regardless of the actual protocol.
func WithRPCSystem(system RPCSystem) Option {
	return &rpcSystemOption{system: system}
}

// RPCSystem represents an RPC system, like ConnectRPC or gRPC, and selects
// the value of the rpc.system.name attribute.
//
//	https://opentelemetry.io/docs/specs/semconv/rpc/
//
// Valid values currently are ConnectRPCSystem and GRPCSystem.
type RPCSystem interface {
	protocol() string
}

type attributeFilterOption struct {
	filterV1 AttributeFilter
	filterV2 func(connect.Spec, attribute.KeyValue) bool
}

func (o *attributeFilterOption) apply(cfg *config) {
	switch {
	case o.filterV1 != nil:
		filter, isClient := o.filterV1, cfg.isClient
		cfg.filterAttribute = func(spec connect.Spec, attr attribute.KeyValue) bool {
			return filter(specToV1(spec, isClient), attr)
		}
	case o.filterV2 != nil:
		cfg.filterAttribute = o.filterV2
	}
}

type propagatorOption struct {
	propagator propagation.TextMapPropagator
}

func (o *propagatorOption) apply(c *config) {
	if o.propagator != nil {
		c.propagator = o.propagator
	}
}

type tracerProviderOption struct {
	provider trace.TracerProvider
}

func (o *tracerProviderOption) apply(c *config) {
	if o.provider != nil {
		c.tracer = o.provider.Tracer(
			instrumentationName,
			trace.WithInstrumentationVersion(semanticVersion),
		)
	}
}

type filterOption struct {
	filterV1 func(context.Context, connectv1.Spec) bool
	filterV2 func(context.Context, connect.Spec) bool
}

func (o *filterOption) apply(cfg *config) {
	switch {
	case o.filterV1 != nil:
		filter, isClient := o.filterV1, cfg.isClient
		cfg.filter = func(ctx context.Context, spec connect.Spec) bool {
			return filter(ctx, specToV1(spec, isClient))
		}
	case o.filterV2 != nil:
		cfg.filter = o.filterV2
	}
}

type meterProviderOption struct {
	provider metric.MeterProvider
}

func (m meterProviderOption) apply(c *config) {
	c.meter = m.provider.Meter(
		instrumentationName,
		metric.WithInstrumentationVersion(semanticVersion),
	)
}

type trustRemoteOption struct{}

func (o *trustRemoteOption) apply(c *config) {
	c.trustRemote = true
}

type traceRequestHeaderOption struct {
	keys []string
}

func (o *traceRequestHeaderOption) apply(c *config) {
	for _, key := range o.keys {
		c.requestHeaderKeys = append(c.requestHeaderKeys, http.CanonicalHeaderKey(key))
	}
}

type traceResponseHeaderOption struct {
	keys []string
}

func (o *traceResponseHeaderOption) apply(c *config) {
	for _, key := range o.keys {
		c.responseHeaderKeys = append(c.responseHeaderKeys, http.CanonicalHeaderKey(key))
	}
}

type durationHistogramOptionsOption struct {
	options []metric.Float64HistogramOption
}

func (o *durationHistogramOptionsOption) apply(c *config) {
	c.durationHistogramOptions = append(c.durationHistogramOptions, o.options...)
}

type propagateResponseHeaderOption struct{}

func (o *propagateResponseHeaderOption) apply(c *config) {
	c.propagateResponseHeader = true
}

type serverPeerAttributesOption struct{}

func (o *serverPeerAttributesOption) apply(c *config) {
	c.serverPeerAttributes = true
}

type rpcSystemOption struct {
	system RPCSystem
}

func (o *rpcSystemOption) apply(c *config) {
	c.rpcSystem = o.system
}

type connectRPCSystem struct{}

func (connectRPCSystem) protocol() string { return connectProtocol }

type gRPCSystem struct{}

func (gRPCSystem) protocol() string { return grpcProtocol }
