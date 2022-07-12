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
	"errors"
	"io"

	"github.com/bufbuild/connect-go"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/stats"
	"go.opencensus.io/tag"
	"go.uber.org/atomic"
)

type clientConnTracker struct {
	connect.StreamingClientConn

	procedure string

	sentCount     atomic.Int64
	receivedCount atomic.Int64
	receivedError atomic.Error
	closedCount   atomic.Uint32
}

func newStreamingClientTracker(clientConnFunc connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		ochttp.SetRoute(ctx, spec.Procedure)
		return &clientConnTracker{
			StreamingClientConn: clientConnFunc(ctx, spec),
			procedure:           spec.Procedure,
		}
	}
}

func (t *clientConnTracker) Send(message any) (retErr error) { // nolint:nonamedreturns
	defer func() {
		if retErr == nil {
			t.sentCount.Inc()
		}
	}()
	return t.StreamingClientConn.Send(message)
}

func (t *clientConnTracker) CloseRequest() error {
	defer func() {
		if t.closedCount.Inc() == 2 {
			t.finishStreamingClientTracking(context.Background())
		}
	}()
	return t.StreamingClientConn.CloseRequest()
}

func (t *clientConnTracker) Receive(message any) (retErr error) { // nolint:nonamedreturns
	defer func() {
		if retErr == nil {
			t.receivedCount.Inc()
			return
		}
		if !errors.Is(retErr, io.EOF) {
			t.receivedError.Store(retErr)
		}
	}()
	return t.StreamingClientConn.Receive(message)
}

func (t *clientConnTracker) CloseResponse() error {
	defer func() {
		if t.closedCount.Inc() == 2 {
			t.finishStreamingClientTracking(context.Background())
		}
	}()
	return t.StreamingClientConn.CloseResponse()
}

func (t *clientConnTracker) finishStreamingClientTracking(ctx context.Context) {
	status := statusOK
	if err := t.receivedError.Load(); err != nil {
		status = connect.CodeOf(err).String()
	}
	tags := []tag.Mutator{
		tag.Upsert(ochttp.KeyClientPath, t.procedure),
		tag.Upsert(KeyClientStatus, status),
	}
	measurements := []stats.Measurement{
		ClientSentMessagesPerRPC.M(t.sentCount.Load()),
		ClientReceivedMessagesPerRPC.M(t.receivedCount.Load()),
	}
	_ = stats.RecordWithOptions(
		ctx,
		stats.WithTags(tags...),
		stats.WithMeasurements(measurements...),
	)
}
