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

type senderTracker struct {
	connect.Sender

	isClient  bool
	procedure string
	sentCount int64
}

func newSenderTracker(ctx context.Context, sender connect.Sender) *senderTracker {
	ochttp.SetRoute(ctx, sender.Spec().Procedure)
	return &senderTracker{
		Sender:    sender,
		isClient:  sender.Spec().IsClient,
		procedure: sender.Spec().Procedure,
	}
}

func (s *senderTracker) Send(message any) error {
	defer func() {
		s.sentCount++
	}()
	return s.Sender.Send(message)
}

func (s *senderTracker) Close(err error) error {
	defer finishSenderTracking(context.Background(), s.isClient, s.procedure, s.sentCount)
	return s.Sender.Close(err)
}

func finishSenderTracking(ctx context.Context, isClient bool, procedure string, sentCount int64) {
	tags := []tag.Mutator{
		tag.Upsert(ochttp.KeyServerRoute, procedure),
	}
	var measurements []stats.Measurement
	if isClient {
		measurements = []stats.Measurement{
			ClientSentMessagesPerRPC.M(sentCount),
		}
	} else {
		measurements = []stats.Measurement{
			ServerSentMessagesPerRPC.M(sentCount),
		}
	}
	_ = stats.RecordWithOptions(
		ctx,
		stats.WithTags(tags...),
		stats.WithMeasurements(measurements...),
	)
}
