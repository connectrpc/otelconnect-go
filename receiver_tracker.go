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

	"connectrpc.com/connect"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/stats"
	"go.opencensus.io/tag"
)

type receiverTracker struct {
	connect.Receiver

	isClient      bool
	procedure     string
	receivedCount int64
}

func newReceiverTracker(ctx context.Context, receiver connect.Receiver) *receiverTracker {
	ochttp.SetRoute(ctx, receiver.Spec().Procedure)
	return &receiverTracker{
		Receiver:  receiver,
		isClient:  receiver.Spec().IsClient,
		procedure: receiver.Spec().Procedure,
	}
}

func (s *receiverTracker) Receive(message any) (retErr error) { // nolint:nonamedreturns
	defer func() {
		if retErr == nil {
			s.receivedCount++
		}
	}()
	return s.Receiver.Receive(message)
}

func (s *receiverTracker) Close() error {
	defer finishReceiverTracking(context.Background(), s.isClient, s.procedure, s.receivedCount)
	return s.Receiver.Close()
}

func finishReceiverTracking(ctx context.Context, isClient bool, procedure string, receivedCount int64) {
	var tags []tag.Mutator
	var measurements []stats.Measurement
	if isClient {
		tags = []tag.Mutator{
			tag.Upsert(ochttp.KeyClientPath, procedure),
		}
		measurements = []stats.Measurement{
			ClientReceivedMessagesPerRPC.M(receivedCount),
		}
	} else {
		tags = []tag.Mutator{
			tag.Upsert(ochttp.KeyServerRoute, procedure),
		}
		measurements = []stats.Measurement{
			ServerReceivedMessagesPerRPC.M(receivedCount),
		}
	}
	_ = stats.RecordWithOptions(
		ctx,
		stats.WithTags(tags...),
		stats.WithMeasurements(measurements...),
	)
}
