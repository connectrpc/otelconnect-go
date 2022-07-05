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
// It adds support for OpenCensus application metrics collection on a Connect server
// or client. It provides multiple tags, stats, and views to be tracked and recorded.
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
	// ClientSentMessagesPerRPC is the client metrics of number of request messages sent by client in the RPC.
	ClientSentMessagesPerRPC = stats.Int64(
		"connect.build/client/sent_messages_per_rpc",
		"Number of request messages sent by client in the RPC (always 1 for non-streaming RPCs).",
		stats.UnitDimensionless,
	)
	// ClientReceivedMessagesPerRPC is the client metrics of number of response messages received by client per RPC.
	ClientReceivedMessagesPerRPC = stats.Int64(
		"connect.build/client/received_messages_per_rpc",
		"Number of response messages received by client per RPC (always 1 for non-streaming RPCs).",
		stats.UnitDimensionless,
	)
)

// The following Connect server metrics are supported for use in custom views.
var (
	// ServerSentMessagesPerRPC is the server metrics of number of response messages sent by server in each RPC.
	ServerSentMessagesPerRPC = stats.Int64(
		"connect.build/server/sent_messages_per_rpc",
		"Number of response messages sent by server in each RPC (always 1 for non-streaming RPCs).",
		stats.UnitDimensionless,
	)
	// ServerReceivedMessagesPerRPC is the server metrics of number of request messages received by server in each RPC.
	ServerReceivedMessagesPerRPC = stats.Int64(
		"connect.build/server/received_messages_per_rpc",
		"Number of request messages received by server in each RPC (always 1 for non-streaming RPCs).",
		stats.UnitDimensionless,
	)
)

var (
	// DefaultLatencyDistribution is the default distribution of RPC latency.
	DefaultLatencyDistribution = view.Distribution(0.01, 0.05, 0.1, 0.3, 0.6, 0.8, 1, 2, 3, 4, 5, 6, 8, 10, 13, 16, 20, 25, 30, 40, 50, 65, 80, 100, 130, 160, 200, 250, 300, 400, 500, 650, 800, 1000, 2000, 5000, 10000, 20000, 50000, 100000)
	// DefaultMessageCountDistribution is the default distribution of message count of each RPC.
	DefaultMessageCountDistribution = view.Distribution(1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536)
)

// Server tags are applied to the context used to process each RPC.
var (
	// KeyServerStatus is the tag's key for status for each RPC in the server side.
	KeyServerStatus = tag.MustNewKey("connect_server_status")
)

// Client tags are applied to the context used to process each RPC.
var (
	// KeyClientStatus is the tag's key for status for each RPC in the client side.
	KeyClientStatus = tag.MustNewKey("connect_client_status")
)

// Package occonnect provides some convenient view for client metrics.
// You still need to register these views for data to actually be collected.
var (
	// ClientRoundtripLatencyView is the distribution view of the end-to-end latency in the client side by RPC method.
	// Purposely reuses the measure from ochttp.ClientRoundtripLatency.
	ClientRoundtripLatencyView = &view.View{
		Name:        "connect.build/client/roundtrip_latency",
		Description: "Distribution of end-to-end latency, by method.",
		TagKeys:     []tag.Key{ochttp.KeyClientPath},
		Measure:     ochttp.ClientRoundtripLatency,
		Aggregation: DefaultLatencyDistribution,
	}
	// ClientCompletedRPCsView is the counting view of the completed RPC in the client side by RPC method and status.
	// Purposely reuses the count from ClientReceivedMessagesPerRPC (note that this is the count of collected records,
	// not the underlying received messages count).
	ClientCompletedRPCsView = &view.View{
		Name:        "connect.build/client/completed_rpcs",
		Description: "Count of RPCs by method and status.",
		TagKeys:     []tag.Key{ochttp.KeyClientPath, KeyClientStatus},
		Measure:     ClientReceivedMessagesPerRPC,
		Aggregation: view.Count(),
	}
	// ClientSentMessagesPerRPCView is the distribution view of the count of sent messages per RPC in the client side by RPC method.
	ClientSentMessagesPerRPCView = &view.View{
		Name:        "connect.build/client/sent_messages_per_rpc",
		Description: "Distribution of sent messages count per RPC, by method.",
		TagKeys:     []tag.Key{ochttp.KeyClientPath},
		Measure:     ClientSentMessagesPerRPC,
		Aggregation: DefaultMessageCountDistribution,
	}
	// ClientReceivedMessagesPerRPCView is the distribution view of the count of received messages per RPC in the client side by RPC method.
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
	// ServerLatencyView is the distribution view of the server latency by RPC method.
	// Purposely reuses the measure from ochttp.ServerLatency.
	ServerLatencyView = &view.View{
		Name:        "connect.build/server/server_latency",
		Description: "Distribution of server latency in milliseconds, by method.",
		TagKeys:     []tag.Key{ochttp.KeyServerRoute},
		Measure:     ochttp.ServerLatency,
		Aggregation: DefaultLatencyDistribution,
	}
	// ServerCompletedRPCsView is the counting view of the completed RPC in the server side by RPC method and status.
	// Purposely reuses the count from ServerSentMessagesPerRPC (note that this is the count of collected records,
	// not the underlying sent messages count).
	ServerCompletedRPCsView = &view.View{
		Name:        "connect.build/server/completed_rpcs",
		Description: "Count of RPCs by method and status.",
		TagKeys:     []tag.Key{ochttp.KeyServerRoute, KeyServerStatus},
		Measure:     ServerSentMessagesPerRPC,
		Aggregation: view.Count(),
	}
	// ServerSentMessagesPerRPCView is the distribution view of the count of sent messages per RPC in the server side by RPC method.
	ServerSentMessagesPerRPCView = &view.View{
		Name:        "connect.build/server/sent_messages_per_rpc",
		Description: "Distribution of messages sent count per RPC, by method.",
		TagKeys:     []tag.Key{ochttp.KeyServerRoute},
		Measure:     ServerSentMessagesPerRPC,
		Aggregation: DefaultMessageCountDistribution,
	}
	// ServerReceivedMessagesPerRPCView is the distribution view of the count of received messages per RPC in the server side by RPC method.
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
