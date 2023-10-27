// Copyright 2022-2023 The Connect Authors
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
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

const (
	metricKeyFormat    = "rpc.%s.%s"
	durationKey        = "duration"
	requestSizeKey     = "request.size"
	responseSizeKey    = "response.size"
	requestsPerRPCKey  = "requests_per_rpc"
	responsesPerRPCKey = "responses_per_rpc"
	messageKey         = "message"
	serverKey          = "server"
	clientKey          = "client"
	requestKey         = "request"
	responseKey        = "response"
	unitDimensionless  = "1"
	unitBytes          = "By"
	unitMilliseconds   = "ms"
)

type instruments struct {
	duration        metric.Int64Histogram
	requestSize     metric.Int64Histogram
	responseSize    metric.Int64Histogram
	requestsPerRPC  metric.Int64Histogram
	responsesPerRPC metric.Int64Histogram
}

// makeInstruments creates the metrics for the interceptor. Any error is reported
// to the global error handler and the interceptor will use the noop metrics.
func makeInstruments(meter metric.Meter, interceptorType string) instruments {
	return instruments{
		duration: makeInt64Histogram(
			meter,
			formatkeys(interceptorType, durationKey),
			metric.WithUnit(unitMilliseconds),
		),
		requestSize: makeInt64Histogram(
			meter,
			formatkeys(interceptorType, requestSizeKey),
			metric.WithUnit(unitBytes),
		),
		responseSize: makeInt64Histogram(
			meter,
			formatkeys(interceptorType, responseSizeKey),
			metric.WithUnit(unitBytes),
		),
		requestsPerRPC: makeInt64Histogram(
			meter,
			formatkeys(interceptorType, requestsPerRPCKey),
			metric.WithUnit(unitDimensionless),
		),
		responsesPerRPC: makeInt64Histogram(
			meter,
			formatkeys(interceptorType, responsesPerRPCKey),
			metric.WithUnit(unitDimensionless),
		),
	}
}

func makeInt64Histogram(meter metric.Meter, name string, options ...metric.Int64HistogramOption) metric.Int64Histogram {
	histogram, err := meter.Int64Histogram(
		name,
		options...,
	)
	if err != nil {
		// Error initializing instruments will not cause the interceptor
		// to fail. Report the error and continue.
		otel.Handle(err)
		return noop.Int64Histogram{}
	}
	return histogram
}

func formatkeys(interceptorType string, metricName string) string {
	return fmt.Sprintf(metricKeyFormat, interceptorType, metricName)
}
