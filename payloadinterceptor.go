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
	"io"
	"sync"
	"time"

	connect "connectrpc.com/connect"
)

type streamingClientInterceptor struct {
	connect.StreamingClientConn

	ctx     context.Context //nolint:containedctx
	state   *state
	startAt time.Time

	requestStarted sync.Once
	mu             sync.Mutex
	requestClosed  bool
	responseClosed bool
}

func (s *streamingClientInterceptor) init() {
	s.requestStarted.Do(func() {
		// Start time on first send.
		s.startAt = s.state.config.now()
		header := s.RequestHeader()
		s.ctx = s.state.start(s.ctx, header)
	})
}

func (s *streamingClientInterceptor) Receive(msg any) error {
	if err := s.StreamingClientConn.Receive(msg); err != nil {
		if !errors.Is(err, io.EOF) {
			s.state.setError(err)
		}
		return err
	}
	// Must already be initialized by a call to Send. This is because
	// Receive blocks until the first message.
	s.state.receive(s.ctx, msg)
	return nil
}

func (s *streamingClientInterceptor) Send(msg any) error {
	s.init()
	if err := s.StreamingClientConn.Send(msg); err != nil {
		if !errors.Is(err, io.EOF) {
			s.state.setError(err)
		}
		return err
	}
	s.state.send(s.ctx, msg)
	return nil
}

func (s *streamingClientInterceptor) CloseRequest() error {
	err := s.StreamingClientConn.CloseRequest()
	s.mu.Lock()
	wasClosed := s.requestClosed && s.responseClosed
	s.requestClosed = true
	hasClosed := !wasClosed && s.responseClosed
	s.mu.Unlock()
	if hasClosed {
		s.onClose()
	}
	return err
}

func (s *streamingClientInterceptor) CloseResponse() error {
	err := s.StreamingClientConn.CloseResponse()
	s.mu.Lock()
	wasClosed := s.requestClosed && s.responseClosed
	s.responseClosed = true
	hasClosed := !wasClosed && s.requestClosed
	s.mu.Unlock()
	if hasClosed {
		s.onClose()
	}
	return err
}

func (s *streamingClientInterceptor) onClose() {
	s.init() // Ensure initialized even under error conditions.
	header := s.ResponseHeader()
	s.state.end(s.ctx, header, s.startAt)
}

type streamingHandlerInterceptor struct {
	connect.StreamingHandlerConn

	ctx   context.Context //nolint:containedctx
	state *state
}

func (s *streamingHandlerInterceptor) Receive(msg any) error {
	if err := s.StreamingHandlerConn.Receive(msg); err != nil {
		if !errors.Is(err, io.EOF) {
			s.state.setError(err)
		}
		return err
	}
	s.state.receive(s.ctx, msg)
	return nil
}

func (s *streamingHandlerInterceptor) Send(msg any) error {
	if err := s.StreamingHandlerConn.Send(msg); err != nil {
		if !errors.Is(err, io.EOF) {
			s.state.setError(err)
		}
		return err
	}
	s.state.send(s.ctx, msg)
	return nil
}
