package otelconnect

import (
	"github.com/bufbuild/connect-go"
	"google.golang.org/protobuf/proto"
	"time"
)

type payloadStreamInterceptorClient struct {
	connect.StreamingClientConn

	ResponseSize    func(int64)
	RequestSize     func(int64)
	RequestsPerRPC  func()
	ResponsesPerRPC func()

	FirstWriteDelay      func(time.Duration)
	InterReceiveDuration func(time.Duration)
	InterSendDuration    func(time.Duration)

	// need to put locks here
	startTime   time.Time
	lastSend    time.Time
	lastReceive time.Time
}

func (p *payloadStreamInterceptorClient) Receive(msg any) error {
	p.RequestsPerRPC()
	p.ResponsesPerRPC()
	err := p.StreamingClientConn.Receive(msg)
	if err != nil {
		return err
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		p.RequestSize(int64(size))
	}
	p.InterReceiveDuration(time.Since(p.lastReceive))
	p.lastReceive = time.Now()

	return nil
}

func (p *payloadStreamInterceptorClient) Send(msg any) error {
	err := p.StreamingClientConn.Send(msg)

	if p.startTime != (time.Time{}) {
		p.FirstWriteDelay(time.Since(p.startTime))
		p.startTime = time.Time{}
	}

	if err != nil {
		return err
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		p.ResponseSize(int64(size))
	}
	p.InterSendDuration(time.Since(p.lastReceive))
	p.lastSend = time.Now()
	return nil
}

type payloadStreamInterceptorHandler struct {
	connect.StreamingHandlerConn

	ResponseSize    func(int64)
	RequestSize     func(int64)
	RequestsPerRPC  func()
	ResponsesPerRPC func()

	FirstWriteDelay      func(time.Duration)
	InterReceiveDuration func(time.Duration)
	InterSendDuration    func(time.Duration)

	// need to put locks here
	startTime   time.Time
	lastSend    time.Time
	lastReceive time.Time
}

func (p *payloadStreamInterceptorHandler) Receive(msg any) error {
	p.RequestsPerRPC()
	p.ResponsesPerRPC()
	err := p.StreamingHandlerConn.Receive(msg)
	if err != nil {
		return err
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		p.RequestSize(int64(size))
	}
	p.InterReceiveDuration(time.Since(p.lastReceive))
	p.lastReceive = time.Now()

	return nil
}

func (p *payloadStreamInterceptorHandler) Send(msg any) error {
	err := p.StreamingHandlerConn.Send(msg)

	if p.startTime != (time.Time{}) {
		p.FirstWriteDelay(time.Since(p.startTime))
		p.startTime = time.Time{}
	}

	if err != nil {
		return err
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		p.ResponseSize(int64(size))
	}
	p.InterSendDuration(time.Since(p.lastReceive))
	p.lastSend = time.Now()
	return nil
}
