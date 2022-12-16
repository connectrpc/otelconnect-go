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
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"github.com/bufbuild/connect-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

const (
	metricKeyFormat = "rpc.%s.%s"

	// Metrics as defined by https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/metrics/semantic_conventions/rpc-metrics.md
	duration        = "duration"
	requestSize     = "request.size"
	responseSize    = "response.size"
	requestsPerRPC  = "requests_per_rpc"
	responsesPerRPC = "responses_per_rpc"
)

type instruments struct {
	sync.Once

	initErr         error
	duration        syncint64.Histogram
	requestSize     syncint64.Histogram
	responseSize    syncint64.Histogram
	requestsPerRPC  syncint64.Histogram
	responsesPerRPC syncint64.Histogram
}

func (i *instruments) init(meter metric.Meter, isClient bool) {
	i.Do(func() {
		intProvider := meter.SyncInt64()
		interceptorType := "server"
		if isClient {
			interceptorType = "client"
		}
		i.duration, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, duration),
			instrument.WithUnit(unit.Milliseconds),
		)
		if i.initErr != nil {
			return
		}
		i.requestSize, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, requestSize),
			instrument.WithUnit(unit.Bytes),
		)
		if i.initErr != nil {
			return
		}
		i.responseSize, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, responseSize),
			instrument.WithUnit(unit.Bytes),
		)
		if i.initErr != nil {
			return
		}
		i.requestsPerRPC, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, requestsPerRPC),
			instrument.WithUnit(unit.Dimensionless),
		)
		if i.initErr != nil {
			return
		}
		i.responsesPerRPC, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, responsesPerRPC),
			instrument.WithUnit(unit.Dimensionless),
		)
	})
}

type interceptor struct {
	config config

	clientInstruments instruments

	handlerInstruments instruments
}

func (i *interceptor) getAndInitInstrument(isClient bool) (*instruments, error) {
	if isClient {
		i.clientInstruments.init(i.config.meter, isClient)
		return &i.clientInstruments, i.clientInstruments.initErr
	}
	i.handlerInstruments.init(i.config.meter, isClient)
	return &i.handlerInstruments, i.handlerInstruments.initErr
}

func newInterceptor(cfg config) *interceptor {
	return &interceptor{
		config: cfg,
	}
}

func (i *interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		requestStartTime := i.config.now()
		isClient := request.Spec().IsClient
		instr, err := i.getAndInitInstrument(isClient)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		req := &Request{
			Spec:   request.Spec(),
			Peer:   request.Peer(),
			Header: request.Header(),
		}
		if i.config.filter != nil {
			if !i.config.filter(ctx, req) {
				return next(ctx, request)
			}
		}
		tracer := i.config.Tracer()
		carrier := propagation.HeaderCarrier(request.Header())
		attrs := attributesFromRequest(req)
		name := strings.TrimLeft(request.Spec().Procedure, "/")
		protocol := protocolToSemConv(request.Peer())
		var span trace.Span
		if request.Spec().IsClient {
			ctx, span = tracer.Start(
				ctx,
				name,
				trace.WithSpanKind(trace.SpanKindClient),
				trace.WithAttributes(attrs...),
			)
		} else {
			ctx = i.config.propagator.Extract(ctx, carrier)
			spanCtx := trace.SpanContextFromContext(ctx)
			ctx, span = tracer.Start(
				trace.ContextWithRemoteSpanContext(ctx, spanCtx),
				name,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(attrs...),
			)
		}
		i.config.propagator.Inject(ctx, carrier)
		defer span.End()
		reqSpanType, resSpanType := semconv.MessageTypeKey.String("RECEIVED"), semconv.MessageTypeKey.String("SENT")
		if request.Spec().IsClient {
			reqSpanType, resSpanType = resSpanType, reqSpanType
		}
		reqSpanAttrs := []attribute.KeyValue{reqSpanType, semconv.MessageIDKey.Int(1)}
		if msg, ok := request.Any().(proto.Message); ok {
			size := proto.Size(msg)
			reqSpanAttrs = append(reqSpanAttrs, semconv.MessageUncompressedSizeKey.Int(size))
		}
		span.AddEvent("message", trace.WithAttributes(reqSpanAttrs...))
		response, err := next(ctx, request)
		codeAttr := statusCodeAttribute(protocol, err)
		attrs = append(attrs, codeAttr)
		if msg, ok := request.Any().(proto.Message); ok {
			size := proto.Size(msg)
			instr.requestSize.Record(ctx, int64(size), attrs...)
		}
		resSpanAttrs := []attribute.KeyValue{resSpanType, semconv.MessageIDKey.Int(1)}
		if err == nil {
			if msg, ok := response.Any().(proto.Message); ok {
				size := proto.Size(msg)
				instr.responseSize.Record(ctx, int64(size), attrs...)
				resSpanAttrs = append(resSpanAttrs, semconv.MessageUncompressedSizeKey.Int(size))
			}
		}
		span.AddEvent("message", trace.WithAttributes(resSpanAttrs...))
		instr.duration.Record(ctx, i.config.now().Sub(requestStartTime).Milliseconds(), attrs...)
		instr.requestsPerRPC.Record(ctx, 1, attrs...)
		instr.responsesPerRPC.Record(ctx, 1, attrs...)
		if err != nil {
			if connectErr := new(connect.Error); errors.As(err, &connectErr) {
				span.SetStatus(codes.Error, connectErr.Message())
			} else {
				span.SetStatus(codes.Error, err.Error())
			}
		}
		span.SetAttributes(codeAttr)
		return response, err
	}
}

// TODO: implement tracing in WrapStreamingClient.
func (i *interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		requestStartTime := i.config.now()
		conn := next(ctx, spec)
		instr, err := i.getAndInitInstrument(spec.IsClient)
		if err != nil {
			return &errorStreamingClientInterceptor{
				StreamingClientConn: conn,
				err:                 connect.NewError(connect.CodeInternal, err),
			}
		}
		req := &Request{
			Spec:   conn.Spec(),
			Peer:   conn.Peer(),
			Header: conn.RequestHeader(),
		}
		if i.config.filter != nil {
			if !i.config.filter(ctx, req) {
				return conn
			}
		}
		state := streamingState{
			protocol: protocolToSemConv(conn.Peer()),
			attrs:    attributesFromRequest(req),
		}
		return &streamingClientInterceptor{
			StreamingClientConn: conn,
			onClose: func() {
				state.attrs = append(state.attrs, statusCodeAttribute(state.protocol, nil))
				instr.duration.Record(ctx, i.config.now().Sub(requestStartTime).Milliseconds(), state.attrs...)
			},
			receive: func(msg any, conn connect.StreamingClientConn) error {
				return state.receive(ctx, instr, msg, conn)
			},
			send: func(msg any, conn connect.StreamingClientConn) error {
				return state.send(ctx, instr, msg, conn)
			},
		}
	}
}

// TODO: implement tracing in WrapStreamingHandler.
func (i *interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		requestStartTime := i.config.now()
		isClient := conn.Spec().IsClient
		instr, err := i.getAndInitInstrument(isClient)
		if err != nil {
			return err
		}
		req := &Request{
			Spec:   conn.Spec(),
			Peer:   conn.Peer(),
			Header: conn.RequestHeader(),
		}
		if i.config.filter != nil {
			if !i.config.filter(ctx, req) {
				return next(ctx, conn)
			}
		}
		state := streamingState{
			protocol: protocolToSemConv(req.Peer),
			attrs:    attributesFromRequest(req),
		}
		streamingHandler := &streamingHandlerInterceptor{
			StreamingHandlerConn: conn,
			receive: func(msg any, conn connect.StreamingHandlerConn) error {
				return state.receive(ctx, instr, msg, conn)
			},
			send: func(msg any, conn connect.StreamingHandlerConn) error {
				return state.send(ctx, instr, msg, conn)
			},
		}
		err = next(ctx, streamingHandler)
		state.attrs = append(state.attrs, statusCodeAttribute(state.protocol, err))
		instr.duration.Record(ctx, i.config.now().Sub(requestStartTime).Milliseconds(), state.attrs...)
		return err
	}
}

func parseAddress(address string) []attribute.KeyValue {
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

func attributesFromRequest(req *Request) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if addr := req.Peer.Addr; addr != "" {
		attrs = append(attrs, parseAddress(addr)...)
	}
	name := strings.TrimLeft(req.Spec.Procedure, "/")
	protocol := protocolToSemConv(req.Peer)
	attrs = append(attrs, semconv.RPCSystemKey.String(protocol))
	attrs = append(attrs, parseProcedure(name)...)
	return attrs
}

func formatkeys(interceptorType string, metricName string) string {
	return fmt.Sprintf(metricKeyFormat, interceptorType, metricName)
}
