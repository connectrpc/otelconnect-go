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

// Package occonnect is a Go implementation of OpenCensus as a Connect interceptor.
package occonnect

import (
	"github.com/bufbuild/connect-go"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

const statusOK = "ok"

// The following Connect client metrics are supported for use in custom views.
var (
	ClientSentMessagesPerRPC = stats.Int64(
		"connect.build/client/sent_messages_per_rpc",
		"Number of request messages sent by client in the RPC (always 1 for non-streaming RPCs).",
		stats.UnitDimensionless,
	)
	ClientReceivedMessagesPerRPC = stats.Int64(
		"connect.build/client/received_messages_per_rpc",
		"Number of response messages received by client per RPC (always 1 for non-streaming RPCs).",
		stats.UnitDimensionless,
	)
)

// The following Connect server metrics are supported for use in custom views.
var (
	ServerSentMessagesPerRPC = stats.Int64(
		"connect.build/server/sent_messages_per_rpc",
		"Number of response messages sent by server in each RPC. Has value 1 for non-streaming RPCs.",
		stats.UnitDimensionless,
	)
	ServerReceivedMessagesPerRPC = stats.Int64(
		"connect.build/server/received_messages_per_rpc",
		"Number of request messages received by server in each RPC. Has value 1 for non-streaming RPCs.",
		stats.UnitDimensionless,
	)
)

var (
	DefaultLatencyDistribution      = view.Distribution(0.01, 0.05, 0.1, 0.3, 0.6, 0.8, 1, 2, 3, 4, 5, 6, 8, 10, 13, 16, 20, 25, 30, 40, 50, 65, 80, 100, 130, 160, 200, 250, 300, 400, 500, 650, 800, 1000, 2000, 5000, 10000, 20000, 50000, 100000)
	DefaultMessageCountDistribution = view.Distribution(1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536)
)

// Server tags are applied to the context used to process each RPC.
var (
	KeyServerMethod = tag.MustNewKey("connect_server_method")
	KeyServerStatus = tag.MustNewKey("connect_server_status")
)

// Client tags are applied to the context used to process each RPC.
var (
	KeyClientMethod = tag.MustNewKey("connect_client_method")
	KeyClientStatus = tag.MustNewKey("connect_client_status")
)

// Package occonnect provides some convenient view for client metrics.
// You still need to register these views for data to actually be collected.
var (
	ClientRoundtripLatencyView = &view.View{
		Name:        "connect.build/http/client/roundtrip_latency",
		Description: "End-to-end latency, by method.",
		TagKeys:     []tag.Key{ochttp.KeyClientPath},
		Measure:     ochttp.ClientRoundtripLatency,
		Aggregation: DefaultLatencyDistribution,
	}

	ClientCompletedRPCsView = &view.View{
		Name:        "connect.build/client/completed_rpcs",
		Description: "Count of RPCs by method and status.",
		TagKeys:     []tag.Key{ochttp.KeyClientPath, KeyClientStatus},
		Measure:     ClientReceivedMessagesPerRPC,
		Aggregation: view.Count(),
	}

	ClientSentMessagesPerRPCView = &view.View{
		Name:        "connect.build/client/sent_messages_per_rpc",
		Description: "Distribution of sent messages count per RPC, by method.",
		TagKeys:     []tag.Key{ochttp.KeyClientPath},
		Measure:     ClientSentMessagesPerRPC,
		Aggregation: DefaultMessageCountDistribution,
	}

	ClientReceivedMessagesPerRPCView = &view.View{
		Name:        "connect.build/client/received_messages_per_rpc",
		Description: "Distribution of received messages count per RPC, by method.",
		TagKeys:     []tag.Key{ochttp.KeyClientPath},
		Measure:     ClientReceivedMessagesPerRPC,
		Aggregation: DefaultMessageCountDistribution,
	}
)

// Package occonnect provides some convenient view for server metrics.
// You still need to register these views for data to actually be collected.
var (
	ServerLatencyView = &view.View{
		Name:        "connect.build/server/server_latency",
		Description: "Distribution of server latency in milliseconds, by method.",
		TagKeys:     []tag.Key{ochttp.KeyServerRoute},
		Measure:     ochttp.ServerLatency,
		Aggregation: DefaultLatencyDistribution,
	}

	ServerCompletedRPCsView = &view.View{
		Name:        "connect.build/server/completed_rpcs",
		Description: "Count of RPCs by method and status.",
		TagKeys:     []tag.Key{ochttp.KeyServerRoute, KeyServerStatus},
		Measure:     ServerSentMessagesPerRPC,
		Aggregation: view.Count(),
	}

	ServerSentMessagesPerRPCView = &view.View{
		Name:        "connect.build/server/sent_messages_per_rpc",
		Description: "Distribution of messages sent count per RPC, by method.",
		TagKeys:     []tag.Key{ochttp.KeyServerRoute},
		Measure:     ServerSentMessagesPerRPC,
		Aggregation: DefaultMessageCountDistribution,
	}

	ServerReceivedMessagesPerRPCView = &view.View{
		Name:        "connect.build/server/received_messages_per_rpc",
		Description: "Distribution of messages received count per RPC, by method.",
		TagKeys:     []tag.Key{ochttp.KeyServerRoute},
		Measure:     ServerReceivedMessagesPerRPC,
		Aggregation: DefaultMessageCountDistribution,
	}
)

// NewInterceptor returns the tracking interceptor of occonnect.
func NewInterceptor() connect.Interceptor {
	return newInterceptor()
}
