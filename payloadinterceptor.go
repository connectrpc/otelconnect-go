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
	"fmt"

	"github.com/bufbuild/connect-go"
)

type streamingClientInterceptor struct {
	connect.StreamingClientConn

	receive        func(any, connect.StreamingClientConn) error
	send           func(any, connect.StreamingClientConn) error
	requestClosed  chan struct{}
	responseClosed chan struct{}
	onClose        func()
}

func (s *streamingClientInterceptor) OnClose() {
	<-s.requestClosed
	<-s.responseClosed
	s.onClose()
}

func (s *streamingClientInterceptor) Receive(msg any) error {
	return s.receive(msg, s.StreamingClientConn)
}

func (s *streamingClientInterceptor) Send(msg any) error {
	return s.send(msg, s.StreamingClientConn)
}

func (s *streamingClientInterceptor) CloseRequest() error {
	defer close(s.requestClosed)
	return s.StreamingClientConn.CloseRequest()
}

func (s *streamingClientInterceptor) CloseResponse() error {
	defer close(s.responseClosed)
	return s.StreamingClientConn.CloseResponse()
}

type errorStreamingClientInterceptor struct {
	connect.StreamingClientConn

	err error
}

func (e *errorStreamingClientInterceptor) Send(any) error {
	return e.err
}

func (e *errorStreamingClientInterceptor) CloseRequest() error {
	if err := e.StreamingClientConn.CloseRequest(); err != nil {
		return fmt.Errorf("%w %s", err, e.err.Error())
	}
	return e.err
}

func (e *errorStreamingClientInterceptor) Receive(any) error {
	return e.err
}

func (e *errorStreamingClientInterceptor) CloseResponse() error {
	if err := e.StreamingClientConn.CloseResponse(); err != nil {
		return fmt.Errorf("%w %s", err, e.err.Error())
	}
	return e.err
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
