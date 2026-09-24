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
	"connectrpc.com/connect/v2"
)

type streamingClientInterceptor struct {
	connect.ClientStream

	receive func(any, connect.ClientStream) error
	send    func(any, connect.ClientStream) error
	onClose func()
}

func (s *streamingClientInterceptor) Receive(msg any) error {
	return s.receive(msg, s.ClientStream)
}

func (s *streamingClientInterceptor) Send(msg any) error {
	return s.send(msg, s.ClientStream)
}

func (s *streamingClientInterceptor) Close() error {
	err := s.ClientStream.Close()
	s.onClose()
	return err
}

type streamingHandlerInterceptor struct {
	connect.ServerStream

	receive func(any, connect.ServerStream) error
	send    func(any, connect.ServerStream) error
}

func (p *streamingHandlerInterceptor) Receive(msg any) error {
	return p.receive(msg, p.ServerStream)
}

func (p *streamingHandlerInterceptor) Send(msg any) error {
	return p.send(msg, p.ServerStream)
}
