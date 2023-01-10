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
	"net"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
)

// AttributeFilter is used to filter attributes out based on the [Request] and [attribute.KeyValue].
// If the filter returns true the attribute will be kept else it will be removed.
// AttributeFilter must be safe to call concurrently.
type AttributeFilter func(*Request, attribute.KeyValue) bool

func (filter AttributeFilter) filter(request *Request, values ...attribute.KeyValue) []attribute.KeyValue {
	if filter == nil {
		return values
	}
	// Assign a new slice of zero length with the same underlying
	// array as the values slice. This avoids unnecessary memory allocations.
	filteredValues := values[:0]
	for _, attr := range values {
		if filter(request, attr) {
			filteredValues = append(filteredValues, attr)
		}
	}
	for i := len(filteredValues); i < len(values); i++ {
		values[i] = attribute.KeyValue{}
	}
	return filteredValues
}

func procedureAttributes(procedure string) []attribute.KeyValue {
	parts := strings.SplitN(procedure, "/", 2)
	var attrs []attribute.KeyValue
	switch len(parts) {
	case 0:
		return attrs // invalid
	case 1:
		// fall back to treating the whole string as the method
		if method := parts[0]; method != "" {
			attrs = append(attrs, semconv.RPCMethodKey.String(method))
		}
	default:
		if svc := parts[0]; svc != "" {
			attrs = append(attrs, semconv.RPCServiceKey.String(svc))
		}
		if method := parts[1]; method != "" {
			attrs = append(attrs, semconv.RPCMethodKey.String(method))
		}
	}
	return attrs
}

func requestAttributes(req *Request) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if addr := req.Peer.Addr; addr != "" {
		attrs = append(attrs, addressAttributes(addr)...)
	}
	name := strings.TrimLeft(req.Spec.Procedure, "/")
	protocol := protocolToSemConv(req.Peer.Protocol)
	attrs = append(attrs, semconv.RPCSystemKey.String(protocol))
	attrs = append(attrs, procedureAttributes(name)...)
	return attrs
}

func addressAttributes(address string) []attribute.KeyValue {
	if host, port, err := net.SplitHostPort(address); err == nil {
		portInt, err := strconv.Atoi(port)
		if err == nil {
			return []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(portInt),
			}
		}
	}
	return []attribute.KeyValue{semconv.NetPeerNameKey.String(address)}
}

func statusCodeAttribute(protocol string, serverErr error) attribute.KeyValue {
	codeKey := attribute.Key("rpc." + protocol + ".status_code")
	// Following the respective specifications, use integers for gRPC codes and
	// strings for Connect codes.
	if strings.HasPrefix(protocol, "grpc") {
		if serverErr != nil {
			return codeKey.Int64(int64(connect.CodeOf(serverErr)))
		}
		return codeKey.Int64(0) // gRPC uses 0 for success
	} else if serverErr != nil {
		return codeKey.String(connect.CodeOf(serverErr).String())
	}
	return codeKey.String("success")
}
