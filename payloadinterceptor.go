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
	"github.com/bufbuild/connect-go"
)

type streamingClientInterceptor struct {
	connect.StreamingClientConn

	receive func(any, connect.StreamingClientConn) error
	send    func(any, connect.StreamingClientConn) error
}

func (p *streamingClientInterceptor) Receive(msg any) error {
	return p.receive(msg, p.StreamingClientConn)
}

func (p *streamingClientInterceptor) Send(msg any) error {
	return p.send(msg, p.StreamingClientConn)
}

type streamingHandlerInterceptor struct {
	connect.StreamingHandlerConn

	receive func(any, connect.StreamingHandlerConn) error
	send    func(any, connect.StreamingHandlerConn) error
}

func (p *streamingHandlerInterceptor) Receive(msg any) error {
	return p.receive(msg, p.StreamingHandlerConn)
}

func (p *streamingHandlerInterceptor) Send(msg any) error {
	return p.send(msg, p.StreamingHandlerConn)
}
