// Copyright 2022-2024 The Connect Authors
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

	connect "connectrpc.com/connect"
	pingv1 "connectrpc.com/otelconnect/internal/gen/observability/ping/v1"
	"connectrpc.com/otelconnect/internal/gen/observability/ping/v1/pingv1connect"
)

const cacheablePingEtag = "ABCDEFGH"

func pingOkay(_ context.Context, req *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
	return connect.NewResponse(&pingv1.PingResponse{
		Id:   req.Msg.GetId(),
		Data: req.Msg.GetData(),
	}), nil
}

func pingFail(_ context.Context, _ *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
	return nil, connect.NewError(connect.CodeDataLoss, errors.New("Oh no"))
}

func pingStreamOkay(
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

func pingStreamFail(
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

func okayPingServer() *pluggablePingServer {
	return &pluggablePingServer{
		ping:       pingOkay,
		pingStream: pingStreamOkay,
	}
}

func failPingServer() *pluggablePingServer {
	return &pluggablePingServer{
		ping:       pingFail,
		pingStream: pingStreamFail,
	}
}

type pluggablePingServer struct {
	pingv1connect.UnimplementedPingServiceHandler

	ping       func(context.Context, *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error)
	pingStream func(context.Context, *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse]) error
}

func (p *pluggablePingServer) Ping(
	ctx context.Context,
	request *connect.Request[pingv1.PingRequest],
) (*connect.Response[pingv1.PingResponse], error) {
	if request.HTTPMethod() == http.MethodGet && request.Header().Get("If-None-Match") == cacheablePingEtag {
		return nil, connect.NewNotModifiedError(nil)
	}
	resp, err := p.ping(ctx, request)
	if err != nil {
		return nil, err
	}
	resp.Header().Set("Etag", cacheablePingEtag)
	return resp, nil
}

func (p *pluggablePingServer) PingStream(
	ctx context.Context,
	stream *connect.BidiStream[pingv1.PingStreamRequest, pingv1.PingStreamResponse],
) error {
	return p.pingStream(ctx, stream)
}
