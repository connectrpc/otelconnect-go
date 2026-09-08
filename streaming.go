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
	"errors"
	"io"
	"slices"
	"sync"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

type streamingState struct {
	mu              sync.Mutex
	spec            connect.Spec
	attributeFilter AttributeFilter
	attributes      []attribute.KeyValue
	peerAttributes  []attribute.KeyValue // spans only, never metrics
	error           error
	labeler         *Labeler
}

func newStreamingState(
	protocol string,
	spec connect.Spec,
	peer connect.Peer,
	attributeFilter AttributeFilter,
	labeler *Labeler,
) *streamingState {
	attributes := make([]attribute.KeyValue, 0, 6) // 4 max request attrs + 2 status attrs
	attributes = addRequestAttributes(protocol, attributes, spec)
	var peerAttributes []attribute.KeyValue
	if spec.IsClient {
		attributes = addAddressAttributes(attributes, peer.Addr, semconv.ServerAddressKey, semconv.ServerPortKey)
	} else {
		peerAttributes = addAddressAttributes(nil, peer.Addr, semconv.NetworkPeerAddressKey, semconv.NetworkPeerPortKey)
	}
	return &streamingState{
		spec:            spec,
		attributeFilter: attributeFilter,
		attributes:      attributeFilter.filter(spec, attributes...),
		peerAttributes:  attributeFilter.filter(spec, peerAttributes...),
		labeler:         labeler,
	}
}

type sendReceiver interface {
	Receive(any) error
	Send(any) error
}

func (s *streamingState) finish(err error) {
	s.error = err
	s.attributes = append(s.attributes, s.attributeFilter.filter(s.spec,
		addStatusAttributes(nil, err)...,
	)...)
}

func (s *streamingState) spanAttributes() []attribute.KeyValue {
	if len(s.peerAttributes) == 0 {
		return s.attributes
	}
	return slices.Concat(s.attributes, s.peerAttributes)
}

func (s *streamingState) metricAttributes() []attribute.KeyValue {
	if s.labeler == nil {
		return s.attributes
	}
	labelerAttrs := s.labeler.Get()
	if len(labelerAttrs) == 0 {
		return s.attributes
	}
	return slices.Concat(s.attributes, labelerAttrs)
}

func (s *streamingState) receive(msg any, conn sendReceiver) error {
	err := conn.Receive(msg)
	if err != nil && !errors.Is(err, io.EOF) {
		s.mu.Lock()
		s.error = err
		s.mu.Unlock()
	}
	return err
}

func (s *streamingState) send(msg any, conn sendReceiver) error {
	err := conn.Send(msg)
	if err != nil && !errors.Is(err, io.EOF) {
		s.mu.Lock()
		s.error = err
		s.mu.Unlock()
	}
	return err
}
