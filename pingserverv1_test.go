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
	"errors"
	"fmt"
	"io"
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect/internal/gen/connectv1/observability/ping/v1/pingv1connect"
	pingv1 "connectrpc.com/otelconnect/internal/gen/observability/ping/v1"
)

const cacheablePingEtagV1 = "ABCDEFGH"

func pingOkayV1(_ context.Context, req *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
	return connect.NewResponse(&pingv1.PingResponse{
		Id:   req.Msg.GetId(),
		Data: req.Msg.GetData(),
	}), nil
}

func pingFailV1(_ context.Context, _ *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
	return nil, connect.NewError(connect.CodeDataLoss, errors.New("Oh no"))
}

func pingStreamOkayV1(
	ctx context.Context,
	stream *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse],
) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		msg, err := stream.Receive()
		if err != nil && errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return fmt.Errorf("receive request: %w", err)
		}
		if err := stream.Send(&pingv1.PingStreamResponse{
			Id:   msg.GetId(),
			Data: msg.GetData(),
		}); err != nil {
			return fmt.Errorf("send response: %w", err)
		}
	}
}

func pingStreamFailV1(
	ctx context.Context,
	stream *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse],
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := stream.Receive()
	if err != nil && errors.Is(err, io.EOF) {
		return nil
	}
	return connect.NewError(connect.CodeDataLoss, errors.New("Oh no"))
}

func okayPingServerV1() *pluggablePingServerV1 {
	return &pluggablePingServerV1{
		ping:       pingOkayV1,
		pingStream: pingStreamOkayV1,
	}
}

func failPingServerV1() *pluggablePingServerV1 {
	return &pluggablePingServerV1{
		ping:       pingFailV1,
		pingStream: pingStreamFailV1,
	}
}

type pluggablePingServerV1 struct {
	pingv1connect.UnimplementedPingServiceHandler

	ping       func(context.Context, *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error)
	pingStream func(context.Context, *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse]) error
}

func (p *pluggablePingServerV1) Ping(
	ctx context.Context,
	request *connect.Request[pingv1.PingRequest],
) (*connect.Response[pingv1.PingResponse], error) {
	if request.HTTPMethod() == http.MethodGet && request.Header().Get("If-None-Match") == cacheablePingEtagV1 {
		return nil, connect.NewNotModifiedError(nil)
	}
	resp, err := p.ping(ctx, request)
	if err != nil {
		return nil, err
	}
	resp.Header().Set("ETag", cacheablePingEtagV1)
	return resp, nil
}

func (p *pluggablePingServerV1) PingStream(
	ctx context.Context,
	stream *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse],
) error {
	return p.pingStream(ctx, stream)
}
