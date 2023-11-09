// Copyright 2022-2023 The Connect Authors
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
	"errors"
	"fmt"
	"net/http"
	"time"

	connect "connectrpc.com/connect"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Interceptor implements [connect.Interceptor] that adds
// OpenTelemetry metrics and tracing to connect handlers and clients.
type Interceptor struct {
	config            config
	clientInstruments instruments
	serverInstruments instruments
}

var _ connect.Interceptor = &Interceptor{}

// NewInterceptor returns an interceptor that implements [connect.Interceptor].
// It adds OpenTelemetry metrics and tracing to connect handlers and clients.
// Use options to configure the interceptor. Any invalid options will cause an
// error to be returned. The interceptor will use the default tracer and meter
// providers. To use a custom tracer or meter provider pass in the
// [WithTracerProvider] or [WithMeterProvider] options. To disable metrics or
// tracing pass in the [WithoutMetrics] or [WithoutTracing] options.
func NewInterceptor(options ...Option) (*Interceptor, error) {
	cfg := config{
		now: time.Now,
		tracer: otel.GetTracerProvider().Tracer(
			instrumentationName,
			trace.WithInstrumentationVersion(semanticVersion),
		),
		propagator: otel.GetTextMapPropagator(),
		meter: otel.GetMeterProvider().Meter(
			instrumentationName,
			metric.WithInstrumentationVersion(semanticVersion)),
	}
	for _, opt := range options {
		opt.apply(&cfg)
	}
	clientInstruments, err := createInstruments(cfg.meter, clientKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create client instruments: %w", err)
	}
	serverInstruments, err := createInstruments(cfg.meter, serverKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create server instruments: %w", err)
	}
	return &Interceptor{
		config:            cfg,
		clientInstruments: clientInstruments,
		serverInstruments: serverInstruments,
	}, nil
}

// getInstruments returns the correct instrumentation for the interceptor.
func (i *Interceptor) getInstruments(isClient bool) *instruments {
	if isClient {
		return &i.clientInstruments
	}
	return &i.serverInstruments
}

// WrapUnary implements otel tracing and metrics for unary handlers.
func (i *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		call := &Request{
			Spec:   req.Spec(),
			Peer:   req.Peer(),
			Header: req.Header(),
		}
		if i.config.filter != nil {
			if !i.config.filter(ctx, call) {
				return next(ctx, req)
			}
		}
		instruments := i.getInstruments(call.Spec.IsClient)
		state := newState(
			call,
			i.config,
			instruments,
		)
		ctx = state.start(ctx, call.Header)
		// Flip call order if on the client.
		msgStart, msgEnd := state.receive, state.send
		if call.Spec.IsClient {
			msgStart, msgEnd = msgEnd, msgStart
		}
		msgStart(ctx, req.Any())
		rsp, err := next(ctx, req)
		if err != nil {
			state.setError(err)
			var header http.Header
			if cerr := (*connect.Error)(nil); errors.As(err, &cerr) {
				header = cerr.Meta()
			}
			state.end(ctx, header)
		} else {
			msgEnd(ctx, rsp.Any())
			state.end(ctx, rsp.Header())
		}
		return rsp, err
	}
}

// WrapStreamingClient implements otel tracing and metrics for streaming connect clients.
func (i *Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		call := &Request{
			Spec:   conn.Spec(),
			Peer:   conn.Peer(),
			Header: conn.RequestHeader(),
		}
		if i.config.filter != nil {
			if !i.config.filter(ctx, call) {
				return conn
			}
		}
		instruments := i.getInstruments(call.Spec.IsClient)
		state := newState(
			call,
			i.config,
			instruments,
		)
		// start is deferred until the first Send for time accuracy
		// and to ensure the request headers are available.
		return &streamingClientInterceptor{
			StreamingClientConn: conn,

			ctx:   ctx,
			state: state,
		}
	}
}

// WrapStreamingHandler implements otel tracing and metrics for streaming connect handlers.
func (i *Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		call := &Request{
			Spec:   conn.Spec(),
			Peer:   conn.Peer(),
			Header: conn.RequestHeader(),
		}
		if i.config.filter != nil {
			if !i.config.filter(ctx, call) {
				return next(ctx, conn)
			}
		}
		instruments := i.getInstruments(call.Spec.IsClient)
		state := newState(
			call,
			i.config,
			instruments,
		)
		ctx = state.start(ctx, call.Header)
		err := next(ctx, &streamingHandlerInterceptor{
			StreamingHandlerConn: conn,

			ctx:   ctx,
			state: state,
		})
		if err != nil {
			state.setError(err)
		}
		state.end(ctx, conn.ResponseHeader())
		return err
	}
}

// protocolToSemConv converts the protocol string to the OpenTelemetry format.
func protocolToSemConv(protocol string) string {
	switch protocol {
	case grpcwebString:
		return grpcwebProtocol
	case grpcProtocol:
		return grpcProtocol
	case connectString:
		return connectProtocol
	default:
		return protocol
	}
}

func spanStatus(protocol string, err error) (codes.Code, string) {
	if err == nil {
		return codes.Unset, ""
	}
	if protocol == connectProtocol && connect.IsNotModifiedError(err) {
		return codes.Unset, ""
	}
	if connectErr := new(connect.Error); errors.As(err, &connectErr) {
		return codes.Error, connectErr.Message()
	}
	return codes.Error, err.Error()
}
