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
)

var _ connect.Interceptor = &interceptor{}

type interceptor struct{}

func newInterceptor() *interceptor {
	return &interceptor{}
}

func (i *interceptor) WrapUnary(unaryFunc connect.UnaryFunc) connect.UnaryFunc {
	return newUnaryTracker(unaryFunc)
}

func (i *interceptor) WrapStreamContext(ctx context.Context) context.Context {
	return ctx
}

func (i *interceptor) WrapStreamSender(ctx context.Context, sender connect.Sender) connect.Sender {
	return newSenderTracker(ctx, sender)
}

func (i *interceptor) WrapStreamReceiver(ctx context.Context, receiver connect.Receiver) connect.Receiver {
	return newReceiverTracker(ctx, receiver)
}
