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
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestAttributeFilter(t *testing.T) {
	t.Parallel()
	filterOdd := AttributeFilter(func(_ connect.Spec, kv attribute.KeyValue) bool {
		return kv.Value.AsInt64()%2 != 0
	})
	assert.Equal(t,
		[]attribute.KeyValue{
			attribute.Int64("one", 1),
			attribute.Int64("three", 3),
		},
		filterOdd.filter(
			connect.Spec{},
			attribute.Int64("zero", 0),
			attribute.Int64("one", 1),
			attribute.Int64("two", 2),
			attribute.Int64("three", 3),
			attribute.Int64("four", 4),
		))
}
