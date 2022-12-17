package otelconnect

import (
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/bufbuild/connect-go"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
)

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
	if addrPort, err := netip.ParseAddrPort(address); err == nil {
		return []attribute.KeyValue{
			semconv.NetPeerIPKey.String(addrPort.Addr().String()),
			semconv.NetPeerPortKey.Int(int(addrPort.Port())),
		}
	}
	if host, port, err := net.SplitHostPort(address); err == nil {
		portint, err := strconv.Atoi(port)
		if err != nil {
			return []attribute.KeyValue{
				semconv.NetPeerNameKey.String(host),
				semconv.NetPeerPortKey.Int(portint),
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
