package otelconnect

import (
	"github.com/bufbuild/connect-go"
	"google.golang.org/protobuf/proto"
	"time"
)

type payloadStreamInterceptorClient struct {
	connect.StreamingClientConn

	rpcServerResponseSize    func(int64)
	rpcServerRequestSize     func(int64)
	rpcServerRequestsPerRPC  func()
	rpcServerResponsesPerRPC func()

	rpcServerFirstWriteDelay      func(time.Duration)
	rpcServerInterReceiveDuration func(time.Duration)
	rpcServerInterSendDuration    func(time.Duration)

	// need to put locks here
	startTime   time.Time
	lastSend    time.Time
	lastReceive time.Time
}

func (p *payloadStreamInterceptorClient) Receive(msg any) error {
	p.rpcServerRequestsPerRPC()
	p.rpcServerResponsesPerRPC()
	err := p.StreamingClientConn.Receive(msg)
	if err != nil {
		return err
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		p.rpcServerRequestSize(int64(size))
	}
	p.rpcServerInterReceiveDuration(time.Since(p.lastReceive))
	p.lastReceive = time.Now()

	return nil
}

func (p *payloadStreamInterceptorClient) Send(msg any) error {
	err := p.StreamingClientConn.Send(msg)

	if p.startTime != (time.Time{}) {
		p.rpcServerFirstWriteDelay(time.Since(p.startTime))
		p.startTime = time.Time{}
	}

	if err != nil {
		return err
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		p.rpcServerResponseSize(int64(size))
	}
	p.rpcServerInterSendDuration(time.Since(p.lastReceive))
	p.lastSend = time.Now()
	return nil
}

type payloadStreamInterceptorHandler struct {
	connect.StreamingHandlerConn

	rpcServerResponseSize    func(int64)
	rpcServerRequestSize     func(int64)
	rpcServerRequestsPerRPC  func()
	rpcServerResponsesPerRPC func()

	rpcServerFirstWriteDelay      func(time.Duration)
	rpcServerInterReceiveDuration func(time.Duration)
	rpcServerInterSendDuration    func(time.Duration)

	// need to put locks here
	startTime   time.Time
	lastSend    time.Time
	lastReceive time.Time
}

func (p *payloadStreamInterceptorHandler) Receive(msg any) error {
	p.rpcServerRequestsPerRPC()
	p.rpcServerResponsesPerRPC()
	err := p.StreamingHandlerConn.Receive(msg)
	if err != nil {
		return err
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		p.rpcServerRequestSize(int64(size))
	}
	p.rpcServerInterReceiveDuration(time.Since(p.lastReceive))
	p.lastReceive = time.Now()

	return nil
}

func (p *payloadStreamInterceptorHandler) Send(msg any) error {
	err := p.StreamingHandlerConn.Send(msg)

	if p.startTime != (time.Time{}) {
		p.rpcServerFirstWriteDelay(time.Since(p.startTime))
		p.startTime = time.Time{}
	}

	if err != nil {
		return err
	}
	if msg, ok := msg.(proto.Message); ok {
		size := proto.Size(msg)
		p.rpcServerResponseSize(int64(size))
	}
	p.rpcServerInterSendDuration(time.Since(p.lastReceive))
	p.lastSend = time.Now()
	return nil
}
