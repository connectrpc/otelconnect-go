package otelconnect

import (
	"github.com/bufbuild/connect-go"
	"google.golang.org/protobuf/proto"
	"net/http"
	"sync"
	"time"
)

var counterMutex sync.Mutex

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
type payloadInterceptor[T Streamer] struct {
	conn    T
	receive func(any, *payloadInterceptor[T])
	send    func(any, *payloadInterceptor[T])

	//handleErr            func(error)
	//responseSize         func(int64)
	//requestSize          func(int64)
	//requestsPerRPC       func()
	//responsesPerRPC      func()
	//firstWriteDelay      func(time.Duration)
	//interReceiveDuration func(time.Duration)
	//interSendDuration    func(time.Duration)
	// tracing

	// need to put locks here
	//startTime   chan time.Time
	//lastSend    chan time.Time
	//lastReceive chan time.Time
}

func (p *payloadInterceptor[T]) Receive(msg any) error {
	p.requestsPerRPC()
	//p.responsesPerRPC()
	err := p.conn.Receive(msg)
	if err != nil {
		p.handleErr(err)
		return err
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		p.requestSize(int64(size))
	}
	if len(p.lastReceive) == 1 {
		p.interReceiveDuration(time.Since(<-p.lastReceive))
	}
	p.lastReceive <- time.Now()

	return nil
}

func (p *payloadInterceptor[T]) Send(msg any) error {
	err := p.conn.Send(msg)
	if len(p.startTime) == 1 {
		p.firstWriteDelay(time.Since(<-p.startTime))
		close(p.startTime)
	}

	if err != nil {
		p.handleErr(err)
		return err
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		p.responseSize(int64(size))
	}
	if len(p.lastSend) == 1 {
		p.interReceiveDuration(time.Since(<-p.lastSend))
	}
	p.lastSend <- time.Now()
	return nil
}
