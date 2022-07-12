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
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/stats"
	"go.opencensus.io/tag"
	"go.uber.org/atomic"
)

type handlerConnTracker struct {
	connect.StreamingHandlerConn

	procedure string

	sentCount     atomic.Int64
	receivedCount atomic.Int64
}

func newStreamingHandlerTracker(handlerConnFunc connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, handlerConn connect.StreamingHandlerConn) (retErr error) { // nolint:nonamedreturns
		ochttp.SetRoute(ctx, handlerConn.Spec().Procedure)
		tracker := &handlerConnTracker{
			StreamingHandlerConn: handlerConn,
			procedure:            handlerConn.Spec().Procedure,
		}
		defer func() {
			tracker.finishStreamingHandlerTracking(ctx, retErr)
		}()
		return handlerConnFunc(ctx, tracker)
	}
}

func (t *handlerConnTracker) Send(message any) (retErr error) { // nolint:nonamedreturns
	defer func() {
		if retErr == nil {
			t.sentCount.Inc()
		}
	}()
	return t.StreamingHandlerConn.Send(message)
}

func (t *handlerConnTracker) Receive(message any) (retErr error) { // nolint:nonamedreturns
	defer func() {
		if retErr == nil {
			t.receivedCount.Inc()
			return
		}
	}()
	return t.StreamingHandlerConn.Receive(message)
}

func (t *handlerConnTracker) finishStreamingHandlerTracking(ctx context.Context, retErr error) {
	status := statusOK
	if retErr != nil {
		status = connect.CodeOf(retErr).String()
	}
	tags := []tag.Mutator{
		tag.Upsert(ochttp.KeyServerRoute, t.procedure),
		tag.Upsert(KeyServerStatus, status),
	}
	measurements := []stats.Measurement{
		ServerSentMessagesPerRPC.M(t.sentCount.Load()),
		ServerReceivedMessagesPerRPC.M(t.receivedCount.Load()),
	}
	_ = stats.RecordWithOptions(
		ctx,
		stats.WithTags(tags...),
		stats.WithMeasurements(measurements...),
	)
}
