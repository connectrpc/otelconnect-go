package otelconnect

import (
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"
)

const (
	metricKeyFormat = "rpc.%s.%s"

	// Metrics as defined by https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/metrics/semantic_conventions/rpc-metrics.md
	durationKey        = "duration"
	requestSizeKey     = "request.size"
	responseSizeKey    = "response.size"
	requestsPerRPCKey  = "requests_per_rpc"
	responsesPerRPCKey = "responses_per_rpc"

	messageKey = "message"
	serverKey  = "server"
	clientKey  = "client"
)

type instruments struct {
	sync.Once

	initErr         error
	duration        syncint64.Histogram
	requestSize     syncint64.Histogram
	responseSize    syncint64.Histogram
	requestsPerRPC  syncint64.Histogram
	responsesPerRPC syncint64.Histogram
}

func (i *instruments) init(meter metric.Meter, isClient bool) {
	i.Do(func() {
		intProvider := meter.SyncInt64()
		interceptorType := serverKey
		if isClient {
			interceptorType = clientKey
		}
		i.duration, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, durationKey),
			instrument.WithUnit(unit.Milliseconds),
		)
		if i.initErr != nil {
			return
		}
		i.requestSize, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, requestSizeKey),
			instrument.WithUnit(unit.Bytes),
		)
		if i.initErr != nil {
			return
		}
		i.responseSize, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, responseSizeKey),
			instrument.WithUnit(unit.Bytes),
		)
		if i.initErr != nil {
			return
		}
		i.requestsPerRPC, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, requestsPerRPCKey),
			instrument.WithUnit(unit.Dimensionless),
		)
		if i.initErr != nil {
			return
		}
		i.responsesPerRPC, i.initErr = intProvider.Histogram(
			formatkeys(interceptorType, responsesPerRPCKey),
			instrument.WithUnit(unit.Dimensionless),
		)
	})
}

func formatkeys(interceptorType string, metricName string) string {
	return fmt.Sprintf(metricKeyFormat, interceptorType, metricName)
}
