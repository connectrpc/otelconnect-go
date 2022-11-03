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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"google.golang.org/protobuf/proto"
	"net"
	"strings"
	"time"
)

const (
	RPCServerDuration        = "rpc.server.duration"
	RPCServerRequestSize     = "rpc.server.request.size"
	RPCServerResponseSize    = "rpc.server.response.size"
	RPCServerRequestsPerRPC  = "rpc.server.requests_per_rpc"
	RPCServerResponsesPerRPC = "rpc.server.responses_per_rpc"

	/* non otel specified metrics */
	RPCServerFirstWriteDelay      = "rpc.server.first_write_delay"
	RPCServerInterReceiveDuration = "rpc.server.inter_receive_duration"
	RPCServerInterSendDuration    = "rpc.server.inter_send_duration"

	RPCClientDuration        = "rpc.client.duration"
	RPCClientRequestSize     = "rpc.client.request.size"
	RPCClientResponseSize    = "rpc.client.response.size"
	RPCClientRequestsPerRPC  = "rpc.client.requests_per_rpc"
	RPCClientResponsesPerRPC = "rpc.client.responses_per_rpc"
)

type metricsConfig struct {
	Filter      func(context.Context, *Request) bool
	Provider    metric.MeterProvider
	propagators propagation.TextMapPropagator
	Meter       metric.Meter
}

type metricsInterceptor struct {
	config                        metricsConfig
	rpcServerDuration             syncint64.Histogram
	rpcServerRequestSize          syncint64.Histogram
	rpcServerResponseSize         syncint64.Histogram
	rpcServerRequestsPerRPC       syncint64.Histogram
	rpcServerResponsesPerRPC      syncint64.Histogram
	rpcServerFirstWriteDelay      syncint64.Histogram
	rpcServerInterReceiveDuration syncint64.Histogram
	rpcServerInterSendDuration    syncint64.Histogram
}

func newMetricsInterceptor(cfg metricsConfig) *metricsInterceptor {
	rpcServerDuration, err := cfg.Meter.SyncInt64().Histogram(RPCServerDuration, instrument.WithUnit(unit.Milliseconds))
	otel.Handle(err)
	rpcServerRequestSize, err := cfg.Meter.SyncInt64().Histogram(RPCServerRequestSize)
	otel.Handle(err)
	rpcServerResponseSize, err := cfg.Meter.SyncInt64().Histogram(RPCServerResponseSize)
	otel.Handle(err)
	rpcServerRequestsPerRPC, err := cfg.Meter.SyncInt64().Histogram(RPCServerRequestsPerRPC)
	otel.Handle(err)
	rpcServerResponsesPerRPC, err := cfg.Meter.SyncInt64().Histogram(RPCServerResponsesPerRPC)
	otel.Handle(err)
	rpcServerFirstWriteDelay, err := cfg.Meter.SyncInt64().Histogram(RPCServerFirstWriteDelay)
	otel.Handle(err)
	rpcServerInterReceiveDuration, err := cfg.Meter.SyncInt64().Histogram(RPCServerInterReceiveDuration)
	otel.Handle(err)
	rpcServerInterSendDuration, err := cfg.Meter.SyncInt64().Histogram(RPCServerInterSendDuration)
	otel.Handle(err)
	return &metricsInterceptor{
		config:                        cfg,
		rpcServerDuration:             rpcServerDuration,
		rpcServerRequestSize:          rpcServerRequestSize,
		rpcServerResponseSize:         rpcServerResponseSize,
		rpcServerRequestsPerRPC:       rpcServerRequestsPerRPC,
		rpcServerResponsesPerRPC:      rpcServerResponsesPerRPC,
		rpcServerFirstWriteDelay:      rpcServerFirstWriteDelay,
		rpcServerInterReceiveDuration: rpcServerInterReceiveDuration,
		rpcServerInterSendDuration:    rpcServerInterSendDuration,
	}
}

func (i *metricsInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return connect.UnaryFunc(func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		requestStartTime := time.Now()
		if i.config.Filter != nil {
			r := &Request{
				Spec:   request.Spec(),
				Peer:   request.Peer(),
				Header: request.Header(),
			}
			if !i.config.Filter(ctx, r) {
				return next(ctx, request)
			}
		}

		serviceMethod := strings.Split(strings.TrimLeft(request.Spec().Procedure, "/"), "/")
		var serviceName, methodName string
		if len(serviceMethod) == 2 {
			serviceName, methodName = serviceMethod[0], serviceMethod[1]
		}

		attrs := []attribute.KeyValue{
			semconv.RPCSystemKey.String("connect"),
			semconv.RPCServiceKey.String(serviceName),
			semconv.RPCMethodKey.String(methodName),
		}
		host, port, err := net.SplitHostPort(request.Peer().Addr)
		if err == nil {
			attrs = append(attrs,
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
			)
		}

		response, err := next(ctx, request)
		if err != nil {
			return nil, err
		}
		if err == nil {
			if msg, ok := response.Any().(proto.Message); ok {
				size := proto.Size(msg)
				i.rpcServerResponseSize.Record(ctx, int64(size), attrs...)
			}
		}
		if msg, ok := request.Any().(proto.Message); ok {
			size := proto.Size(msg)
			i.rpcServerRequestSize.Record(ctx, int64(size))
		}

		i.rpcServerDuration.Record(ctx, time.Since(requestStartTime).Milliseconds(), attrs...)
		return response, err
	})
}

func (i *metricsInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		return next(ctx, spec)
	}
}

type payloadStreamInterceptor struct {
	connect.StreamingHandlerConn
	rpcServerResponseSize    func(int64)
	rpcServerRequestSize     func(int64)
	rpcServerRequestsPerRPC  func()
	rpcServerResponsesPerRPC func()

	rpcServerFirstWriteDelay      func(time.Duration)
	rpcServerInterReceiveDuration func(time.Duration)
	rpcServerInterSendDuration    func(time.Duration)

	startTime   time.Time
	lastSend    time.Time
	lastReceive time.Time
}

func (p *payloadStreamInterceptor) Receive(msg any) error {
	p.rpcServerRequestsPerRPC()
	p.rpcServerResponsesPerRPC()
	err := p.StreamingHandlerConn.Receive(msg)
	if err != nil {
		return err
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		p.rpcServerRequestSize(int64(size))
	}
	p.rpcServerInterReceiveDuration(time.Since(p.lastReceive))
	p.lastReceive = time.Now()
	return nil
}

func (p *payloadStreamInterceptor) Send(msg any) error {
	err := p.StreamingHandlerConn.Send(msg)

	if p.startTime != (time.Time{}) {
		p.rpcServerFirstWriteDelay(time.Since(p.startTime))
		p.startTime = time.Time{}
	}

	if err != nil {
		return err
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		p.rpcServerResponseSize(int64(size))
	}
	p.rpcServerInterSendDuration(time.Since(p.lastReceive))
	p.lastSend = time.Now()
	return nil
}

func (i *metricsInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		requestStartTime := time.Now()
		if i.config.Filter != nil {
			r := &Request{
				Spec:   conn.Spec(),
				Peer:   conn.Peer(),
				Header: conn.RequestHeader(),
			}
			if !i.config.Filter(ctx, r) {
				return next(ctx, conn)
			}
		}

		serviceMethod := strings.Split(strings.TrimLeft(conn.Spec().Procedure, "/"), "/")
		var serviceName, methodName string
		if len(serviceMethod) == 2 {
			serviceName, methodName = serviceMethod[0], serviceMethod[1]
		}

		attrs := []attribute.KeyValue{
			semconv.RPCSystemKey.String("connect"),
			semconv.RPCServiceKey.String(serviceName),
			semconv.RPCMethodKey.String(methodName),
		}
		host, port, err := net.SplitHostPort(conn.Peer().Addr)
		if err == nil {
			attrs = append(attrs,
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.String(port),
			)
		}

		i.rpcServerDuration.Record(ctx, time.Since(requestStartTime).Milliseconds(), attrs...)

		return next(ctx, &payloadStreamInterceptor{
			StreamingHandlerConn: conn,
			startTime:            requestStartTime,
			rpcServerRequestsPerRPC: func() {
				i.rpcServerRequestsPerRPC.Record(ctx, 1, attrs...)
			},
			rpcServerResponsesPerRPC: func() {
				i.rpcServerResponsesPerRPC.Record(ctx, 1, attrs...)
			},
			rpcServerResponseSize: func(size int64) {
				i.rpcServerResponseSize.Record(ctx, int64(size), attrs...)
			},
			rpcServerRequestSize: func(size int64) {
				i.rpcServerRequestSize.Record(ctx, int64(size), attrs...)
			},
			rpcServerFirstWriteDelay: func(t time.Duration) {
				i.rpcServerFirstWriteDelay.Record(ctx, t.Milliseconds(), attrs...)
			},
			rpcServerInterReceiveDuration: func(t time.Duration) {
				i.rpcServerInterReceiveDuration.Record(ctx, t.Milliseconds(), attrs...)
			},
			rpcServerInterSendDuration: func(t time.Duration) {
				i.rpcServerInterSendDuration.Record(ctx, t.Milliseconds(), attrs...)
			},
		})
	}
}
