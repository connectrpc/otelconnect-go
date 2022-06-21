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

package occonnect_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/bufbuild/connect-go"
	occonnect "github.com/bufbuild/connect-opencensus-go"
	pingv1 "github.com/bufbuild/connect-opencensus-go/internal/gen/connect/ping/v1"
	"github.com/bufbuild/connect-opencensus-go/internal/gen/connect/ping/v1/pingv1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/trace"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func TestOCConnectInterceptor(t *testing.T) {
	t.Parallel()
	// register all occonnect server views
	serverViews := []*view.View{
		occonnect.ServerLatencyView,
		occonnect.ServerCompletedRPCsView,
		occonnect.ServerSentMessagesPerRPCView,
		occonnect.ServerReceivedMessagesPerRPCView,
	}
	err := view.Register(serverViews...)
	require.NoError(t, err)
	defer view.Unregister(serverViews...)

	// register all occonnect client views
	clientViews := []*view.View{
		occonnect.ClientRoundtripLatencyView,
		occonnect.ClientCompletedRPCsView,
		occonnect.ClientSentMessagesPerRPCView,
		occonnect.ClientReceivedMessagesPerRPCView,
	}
	err = view.Register(clientViews...)
	require.NoError(t, err)
	defer view.Unregister(clientViews...)

	client, done := newTestClient(t)
	defer done()

	ctx := context.Background()
	pingRequest := &pingv1.PingRequest{
		Number: 1,
		Text:   "123",
	}
	_, err = client.Ping(ctx, connect.NewRequest(pingRequest))
	require.NoError(t, err)
	assertViewData(
		t,
		1, // length
		1, // expected count
		1, // server sent count
		1, // client sent count
	)

	_, err = client.Ping(ctx, connect.NewRequest(pingRequest))
	require.NoError(t, err)
	assertViewData(
		t,
		1, // length
		2, // expected count
		2, // server sent count
		2, // client sent count
	)

	sumStream := client.Sum(ctx)
	for _, num := range []int64{1, 2, 3, 4, 5} {
		sumRequest := &pingv1.SumRequest{
			Number: num,
		}
		err = sumStream.Send(sumRequest)
		require.NoError(t, err)
	}
	sum, err := sumStream.CloseAndReceive()
	require.NoError(t, err)
	assert.Equal(t, int64(15), sum.Msg.Sum)
	assertViewData(
		t,
		2, // length
		3, // expected count
		3, // server sent count
		7, // client sent count
	)

	countUpRequest := &pingv1.CountUpRequest{
		Number: 7,
	}
	countUpStream, err := client.CountUp(ctx, connect.NewRequest(countUpRequest))
	require.NoError(t, err)
	for countUpStream.Receive() {
		require.NoError(t, countUpStream.Err())
	}
	err = countUpStream.Close()
	require.NoError(t, err)
	assertViewData(
		t,
		3,  // length
		4,  // expected count
		10, // server sent count
		8,  // client sent count
	)

	cumSumStream := client.CumSum(ctx)
	for _, num := range []int64{1, 2, 3, 4, 5} {
		cumSumRequest := &pingv1.CumSumRequest{
			Number: num,
		}
		err = cumSumStream.Send(cumSumRequest)
		require.NoError(t, err)
		_, err = cumSumStream.Receive()
		require.NoError(t, err)
	}
	err = cumSumStream.CloseSend()
	require.NoError(t, err)
	err = cumSumStream.CloseReceive()
	require.NoError(t, err)
	assertViewData(
		t,
		4,  // length
		5,  // expected count
		15, // server sent count
		13, // client sent count
	)
}

func assertViewData(t *testing.T, length int, expectedCount int64, serverSentCount float64, clientSentCount float64) {
	t.Helper()
	// assert server view data
	assertCount(t, occonnect.ServerLatencyView.Name, length, expectedCount)
	assertCount(t, occonnect.ServerCompletedRPCsView.Name, length, expectedCount)
	assertDistributionData(t, occonnect.ServerSentMessagesPerRPCView.Name, length, expectedCount, serverSentCount)
	assertDistributionData(t, occonnect.ServerReceivedMessagesPerRPCView.Name, length, expectedCount, clientSentCount)

	// assert client view data
	assertCount(t, occonnect.ClientRoundtripLatencyView.Name, length, expectedCount)
	assertCount(t, occonnect.ClientCompletedRPCsView.Name, length, expectedCount)
	assertDistributionData(t, occonnect.ClientSentMessagesPerRPCView.Name, length, expectedCount, clientSentCount)
	assertDistributionData(t, occonnect.ClientReceivedMessagesPerRPCView.Name, length, expectedCount, serverSentCount)
}

func assertCount(t *testing.T, viewName string, length int, expectedCount int64) {
	t.Helper()
	v := view.Find(viewName)
	if v == nil {
		t.Errorf("view not found %q", viewName)
		return
	}
	rows, err := view.RetrieveData(v.Name)
	require.NoError(t, err)
	require.Len(t, rows, length)
	count := int64(0)
	for _, row := range rows {
		switch data := row.Data.(type) {
		case *view.CountData:
			count += data.Value
		case *view.DistributionData:
			count += data.Count
		default:
			t.Errorf("Unknown data type: %v", data)
		}
	}
	assert.Equal(t, expectedCount, count)
}

func assertDistributionData(t *testing.T, viewName string, length int, expectedCount int64, expectedSum float64) {
	t.Helper()
	v := view.Find(viewName)
	if v == nil {
		t.Errorf("view not found %q", viewName)
		return
	}
	rows, err := view.RetrieveData(v.Name)
	require.NoError(t, err)
	require.Len(t, rows, length)
	count := int64(0)
	sum := float64(0)
	for _, row := range rows {
		data, ok := row.Data.(*view.DistributionData)
		require.True(t, ok)
		count += data.Count
		sum += data.Sum()
	}
	assert.Equal(t, expectedCount, count)
	assert.Equal(t, expectedSum, sum)
}

func newTestClient(t *testing.T) (pingv1connect.PingServiceClient, func()) {
	t.Helper()
	// initialize server
	mux := http.NewServeMux()
	mux.Handle(pingv1connect.NewPingServiceHandler(
		&testPingServer{},
		connect.WithInterceptors(
			occonnect.NewInterceptor(),
		),
	))
	h2Server := &http.Server{
		Handler: h2c.NewHandler(
			&ochttp.Handler{
				Handler: mux,
				StartOptions: trace.StartOptions{
					Sampler: trace.NeverSample(),
				},
			},
			&http2.Server{},
		),
	}
	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	go func() {
		_ = h2Server.Serve(listener)
	}()
	// Initialize client
	transport := &ochttp.Transport{
		Base: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(netw, addr string, cfg *tls.Config) (net.Conn, error) {
				return net.Dial(netw, addr)
			},
		},
	}
	client := pingv1connect.NewPingServiceClient(
		&http.Client{Transport: transport},
		"http://"+listener.Addr().String(),
		connect.WithInterceptors(
			occonnect.NewInterceptor(),
		),
	)
	cleanup := func() {
		err = h2Server.Shutdown(context.Background())
		require.NoError(t, err)
	}
	return client, cleanup
}

type testPingServer struct {
	pingv1connect.UnimplementedPingServiceHandler
}

func (s *testPingServer) Ping(context.Context, *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
	return connect.NewResponse(&pingv1.PingResponse{
		Number: 123,
		Text:   "123",
	}), nil
}

func (s *testPingServer) Sum(
	ctx context.Context,
	stream *connect.ClientStream[pingv1.SumRequest],
) (*connect.Response[pingv1.SumResponse], error) {
	var sum int64
	for stream.Receive() {
		sum += stream.Msg().Number
	}
	if stream.Err() != nil {
		return nil, stream.Err()
	}
	response := connect.NewResponse(&pingv1.SumResponse{Sum: sum})
	return response, nil
}

func (s *testPingServer) CountUp(
	ctx context.Context,
	request *connect.Request[pingv1.CountUpRequest],
	stream *connect.ServerStream[pingv1.CountUpResponse],
) error {
	if request.Msg.Number <= 0 {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"number must be positive: got %v",
			request.Msg.Number,
		))
	}
	for i := int64(1); i <= request.Msg.Number; i++ {
		if err := stream.Send(&pingv1.CountUpResponse{Number: i}); err != nil {
			return err
		}
	}
	return nil
}

func (s *testPingServer) CumSum(
	ctx context.Context,
	stream *connect.BidiStream[pingv1.CumSumRequest, pingv1.CumSumResponse],
) error {
	var sum int64
	for {
		msg, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		sum += msg.Number
		if err := stream.Send(&pingv1.CumSumResponse{Sum: sum}); err != nil {
			return err
		}
	}
}
