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
	"net/http"

	"github.com/bufbuild/connect-go"
)

type payloadInterceptor[T streamer] struct {
	conn    T // if this could be embedded then the other types wouldn't be needed
	receive func(any, T) error
	send    func(any, T) error
}

type streamer interface {
	Send(any) error
	Receive(any) error
	RequestHeader() http.Header
	ResponseHeader() http.Header
}

func (p *payloadInterceptor[T]) Receive(msg any) error {
	return p.receive(msg, p.conn)
}

func (p *payloadInterceptor[T]) Send(msg any) error {
	return p.send(msg, p.conn)
}

type streamingClientInterceptor struct {
	connect.StreamingClientConn
	payloadInterceptor[connect.StreamingClientConn]
}

func (p *streamingClientInterceptor) Receive(msg any) error {
	return p.payloadInterceptor.Receive(msg)
}

func (p *streamingClientInterceptor) Send(msg any) error {
	return p.payloadInterceptor.Send(msg)
}

type streamingHandlerInterceptor struct {
	connect.StreamingHandlerConn
	payloadInterceptor[connect.StreamingHandlerConn]
}

func (p *streamingHandlerInterceptor) Receive(msg any) error {
	return p.payloadInterceptor.Receive(msg)
}

func (p *streamingHandlerInterceptor) Send(msg any) error {
	return p.payloadInterceptor.Send(msg)
}
