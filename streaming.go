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
	"errors"
	"io"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"
)

type streamingState struct {
	mu              sync.Mutex
	attributes      []attribute.KeyValue
	error           error
	sentCounter     int
	receivedCounter int
}

type sendReceiver interface {
	Receive(any) error
	Send(any) error
}

func (s *streamingState) receive(msg any, conn sendReceiver, protocol string) (int, func(), error) {
	err := conn.Receive(msg)
	s.mu.Lock()
	if errors.Is(err, io.EOF) {
		return 0, s.mu.Unlock, err
	}
	if err != nil {
		s.error = err
		// If error add it to the attributes because the stream is about to terminate.
		// If no error don't add anything because status only exists at end of stream.
		s.attributes = append(s.attributes, statusCodeAttribute(protocol, err))
		return 0, s.mu.Unlock, err
	}
	var size int
	if msg, ok := msg.(proto.Message); ok {
		size = proto.Size(msg)
	}
	s.receivedCounter++
	return size, s.mu.Unlock, err
}

func (s *streamingState) send(msg any, conn sendReceiver, protocol string) (int, func(), error) {
	err := conn.Send(msg)
	s.mu.Lock()
	if errors.Is(err, io.EOF) {
		return 0, s.mu.Unlock, err
	}
	if err != nil {
		s.error = err
		// If error add it to the attributes because the stream is about to terminate.
		// If no error don't add anything because status only exists at end of stream.
		s.attributes = append(s.attributes, statusCodeAttribute(protocol, err))
		return 0, s.mu.Unlock, err
	}
	var size int
	if msg, ok := msg.(proto.Message); ok {
		size = proto.Size(msg)
	}
	s.sentCounter++
	return size, s.mu.Unlock, err
}
