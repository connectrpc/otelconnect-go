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

	"github.com/bufbuild/connect-go"
)

type metricsConfig struct {
	Filter func(context.Context, *Request) bool
}

type metricsInterceptor struct {
	config metricsConfig
}

func (i *metricsInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return next // TODO
}

func (i *metricsInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next // TODO
}

func (i *metricsInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next // TODO
}
