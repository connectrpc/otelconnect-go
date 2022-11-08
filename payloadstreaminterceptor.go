package otelconnect

import (
	"github.com/bufbuild/connect-go"
	"net/http"
)

type streamingClientInterceptor struct {
	connect.StreamingClientConn
	payloadInterceptor[connect.StreamingClientConn]
}

func (p *streamingClientInterceptor) Receive(msg any) error {
	return p.conn.Receive(msg)
}

func (p *streamingClientInterceptor) Send(msg any) error {
	return p.conn.Send(msg)
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

type Streamer interface {
	Send(any) error
	Receive(any) error
	RequestHeader() http.Header
	ResponseHeader() http.Header
}

// payloadInterceptor is necessary because go generics don't allow you to embed a generic type.
type payloadInterceptor[T any] struct {
	conn    T
	receive func(any, *payloadInterceptor[T]) error
	send    func(any, *payloadInterceptor[T]) error
}

func (p *payloadInterceptor[T]) Receive(msg any) error {
	return p.receive(msg, p)
}

func (p *payloadInterceptor[T]) Send(msg any) error {
	return p.send(msg, p)
}
