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
	"context"
	"testing"

	connectv1 "connectrpc.com/connect"
	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

const (
	testSchema    = "schema"
	testProcedure = "/pkg.Service/Method"
)

func TestFilterSpecVersions(t *testing.T) {
	t.Parallel()
	spec := connect.Spec{
		StreamType:       connect.StreamTypeBidi,
		IdempotencyLevel: connect.IdempotencyNoSideEffects,
		Schema:           testSchema,
		Procedure:        testProcedure,
	}
	for _, isClient := range []bool{false, true} {
		wantV1 := connectv1.Spec{
			StreamType:       connectv1.StreamTypeBidi,
			IdempotencyLevel: connectv1.IdempotencyNoSideEffects,
			Schema:           testSchema,
			Procedure:        testProcedure,
			IsClient:         isClient,
		}
		var gotV1 connectv1.Spec
		var gotV2 connect.Spec
		for _, option := range []Option{
			WithFilter(func(_ context.Context, spec connectv1.Spec) bool {
				gotV1 = spec
				return true
			}),
			WithAttributeFilter(AttributeFilter(func(spec connectv1.Spec, _ attribute.KeyValue) bool {
				gotV1 = spec
				return true
			})),
			WithAttributeFilter(func(spec connectv1.Spec, _ attribute.KeyValue) bool {
				gotV1 = spec
				return true
			}),
		} {
			gotV1 = connectv1.Spec{}
			cfg := config{isClient: isClient}
			option.apply(&cfg)
			if cfg.filter != nil {
				assert.True(t, cfg.filter(t.Context(), spec))
			} else {
				assert.Equal(t, []attribute.KeyValue{attribute.Int("a", 1)}, cfg.filterAttribute.filter(spec, attribute.Int("a", 1)))
			}
			assert.Equal(t, wantV1, gotV1)
		}
		for _, option := range []Option{
			WithFilter(func(_ context.Context, spec connect.Spec) bool {
				gotV2 = spec
				return true
			}),
			WithAttributeFilter(func(spec connect.Spec, _ attribute.KeyValue) bool {
				gotV2 = spec
				return true
			}),
		} {
			gotV2 = connect.Spec{}
			cfg := config{isClient: isClient}
			option.apply(&cfg)
			if cfg.filter != nil {
				assert.True(t, cfg.filter(t.Context(), spec))
			} else {
				assert.Len(t, cfg.filterAttribute.filter(spec, attribute.Int("a", 1)), 1)
			}
			assert.Equal(t, spec, gotV2)
		}
	}
}

func TestSpecFromV1(t *testing.T) {
	t.Parallel()
	specV1 := connectv1.Spec{
		StreamType:       connectv1.StreamTypeServer,
		IdempotencyLevel: connectv1.IdempotencyIdempotent,
		Schema:           testSchema,
		Procedure:        testProcedure,
		IsClient:         true,
	}
	assert.Equal(t, specV1, specToV1(specFromV1(specV1), true))
}
