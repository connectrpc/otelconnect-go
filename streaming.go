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
	"io"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"
)

type streamingState struct {
	protocol string
	mu       sync.Mutex
	attrs    []attribute.KeyValue
}

type sendReceiver interface {
	Receive(any) error
	Send(any) error
}

func (s *streamingState) receive(ctx context.Context, instr *instruments, msg any, conn sendReceiver) error {
	err := conn.Receive(msg)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil && !errors.Is(err, io.EOF) {
		s.attrs = append(s.attrs, statusCodeAttribute(s.protocol, err))
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		instr.requestSize.Record(ctx, int64(size), s.attrs...)
	}
	instr.requestsPerRPC.Record(ctx, 1, s.attrs...)
	instr.responsesPerRPC.Record(ctx, 1, s.attrs...)
	return err
}

func (s *streamingState) send(ctx context.Context, instr *instruments, msg any, conn sendReceiver) error {
	err := conn.Send(msg)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil && !errors.Is(err, io.EOF) {
		s.attrs = append(s.attrs, statusCodeAttribute(s.protocol, err))
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		instr.responseSize.Record(ctx, int64(size), s.attrs...)
	}
	return err
}
