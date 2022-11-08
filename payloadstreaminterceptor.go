package otelconnect

import (
	"github.com/bufbuild/connect-go"
	"net/http"
)

type payloadInterceptor[T streamer] struct {
	conn    T // if this could be embedded then the other types wouldn't be needed
	receive func(any, T) error
	send    func(any, T) error
}

type streamer interface {
	Send(any) error
	Receive(any) error
	RequestHeader() http.Header
	ResponseHeader() http.Header
}

func (p *payloadInterceptor[T]) Receive(msg any) error {
	return p.receive(msg, p.conn)
}

func (p *payloadInterceptor[T]) Send(msg any) error {
	return p.send(msg, p.conn)
}

type streamingClientInterceptor struct {
	connect.StreamingClientConn
	payloadInterceptor[connect.StreamingClientConn]
}

func (p *streamingClientInterceptor) Receive(msg any) error {
	return p.payloadInterceptor.Receive(msg)
}

func (p *streamingClientInterceptor) Send(msg any) error {
	return p.payloadInterceptor.Send(msg)
}

type streamingHandlerInterceptor struct {
	connect.StreamingHandlerConn
	payloadInterceptor[connect.StreamingHandlerConn]
}

func (p *streamingHandlerInterceptor) Receive(msg any) error {
	return p.payloadInterceptor.Receive(msg)
}

func (p *streamingHandlerInterceptor) Send(msg any) error {
	return p.payloadInterceptor.Send(msg)
}
