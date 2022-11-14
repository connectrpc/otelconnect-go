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
	"net/http"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"
)

type streamingState struct {
	protocol string
	mut      sync.Mutex
	attrs    []attribute.KeyValue
}

type sendReceiver interface {
	Receive(any) error
	Send(any) error
	RequestHeader() http.Header
}

func (s *streamingState) receive(ctx context.Context, intercept *interceptor, msg any, conn sendReceiver) error {
	err := conn.Receive(msg)
	s.mut.Lock()
	defer s.mut.Unlock()
	if err != nil {
		s.attrs = append(s.attrs, statusCodeAttribute(s.protocol, err))
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		intercept.requestSize.Record(ctx, int64(size), s.attrs...)
	}
	intercept.requestsPerRPC.Record(ctx, 1, s.attrs...)
	intercept.responsesPerRPC.Record(ctx, 1, s.attrs...)
	return err
}

func (s *streamingState) send(ctx context.Context, intercept *interceptor, msg any, conn sendReceiver) error {
	err := conn.Send(msg)
	s.mut.Lock()
	defer s.mut.Unlock()
	if err != nil {
		s.attrs = append(s.attrs, statusCodeAttribute(parseProtocol(conn.RequestHeader()), err))
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		intercept.responseSize.Record(ctx, int64(size), s.attrs...)
	}
	return err
}
