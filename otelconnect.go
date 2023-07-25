// Copyright 2022-2023 Buf Technologies, Inc.
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
	"connectrpc.com/otelconnect"
)

// Interceptor implements [connect.Interceptor] that adds
// OpenTelemetry metrics and tracing to connect handlers and clients.
type Interceptor = otelconnect.Interceptor

// NewInterceptor constructs and returns an Interceptor which implements [connect.Interceptor]
// that adds OpenTelemetry metrics and tracing to Connect handlers and clients.
func NewInterceptor(options ...Option) *Interceptor {
	return otelconnect.NewInterceptor(options...)
}

// AttributeFilter is used to filter attributes out based on the [Request] and [attribute.KeyValue].
// If the filter returns true the attribute will be kept else it will be removed.
// AttributeFilter must be safe to call concurrently.
type AttributeFilter = otelconnect.AttributeFilter

// Request is the information about each RPC available to filter functions. It
// contains the common subset of [connect.AnyRequest],
// [connect.StreamingClientConn], and [connect.StreamingHandlerConn].
type Request = otelconnect.Request
