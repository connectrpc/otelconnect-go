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

// makeInstruments creates the metrics for the interceptor. If the metrics
// cannot be created, an error is returned and the instruments will use the
// noop metrics.
func makeInstruments(meter metric.Meter, interceptorType string) (instruments, error) {
	instruments := instruments{
		duration:        noop.Int64Histogram{},
		requestSize:     noop.Int64Histogram{},
		responseSize:    noop.Int64Histogram{},
		requestsPerRPC:  noop.Int64Histogram{},
		responsesPerRPC: noop.Int64Histogram{},
	}
	duration, err := meter.Int64Histogram(
		formatkeys(interceptorType, durationKey),
		metric.WithUnit(unitMilliseconds),
	)
	if err != nil {
		return instruments, err
	}
	instruments.duration = duration
	requestSize, err := meter.Int64Histogram(
		formatkeys(interceptorType, requestSizeKey),
		metric.WithUnit(unitBytes),
	)
	if err != nil {
		return instruments, err
	}
	instruments.requestSize = requestSize
	responseSize, err := meter.Int64Histogram(
		formatkeys(interceptorType, responseSizeKey),
		metric.WithUnit(unitBytes),
	)
	if err != nil {
		return instruments, err
	}
	instruments.responseSize = responseSize
	requestsPerRPC, err := meter.Int64Histogram(
		formatkeys(interceptorType, requestsPerRPCKey),
		metric.WithUnit(unitDimensionless),
	)
	if err != nil {
		return instruments, err
	}
	instruments.requestsPerRPC = requestsPerRPC
	responsesPerRPC, err := meter.Int64Histogram(
		formatkeys(interceptorType, responsesPerRPCKey),
		metric.WithUnit(unitDimensionless),
	)
	instruments.responsesPerRPC = responsesPerRPC
	if err != nil {
		return instruments, err
	}
	return instruments, nil
}

func formatkeys(interceptorType string, metricName string) string {
	return fmt.Sprintf(metricKeyFormat, interceptorType, metricName)
}
