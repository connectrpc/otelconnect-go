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

package occonnect

import (
	"context"

	"github.com/bufbuild/connect-go"
)

type receiverTracker struct {
	connect.Receiver
}

func newReceiverTracker(ctx context.Context, receiver connect.Receiver) *receiverTracker {
	// TODO: add tags to ctx
	return &receiverTracker{
		Receiver: receiver,
	}
}

func (s *receiverTracker) Receive(message any) error {
	// TODO: tracking
	return s.Receiver.Receive(message)
}

func (s *receiverTracker) Close() error {
	// TODO: tracking
	return s.Receiver.Close()
}
