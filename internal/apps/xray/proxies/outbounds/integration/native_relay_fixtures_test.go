//go:build integration && linux

package integration_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	handlers "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// The relay counts actual forwarded requests to standalone Xray. Losing an add
// acknowledgement happens only after the real engine returned that applied add.
type nativeRelay struct {
	handlers.UnimplementedHandlerServiceServer
	target               handlers.HandlerServiceClient
	lists, adds, removes atomic.Int64
	loseAdd              atomic.Bool
}

func newRelay(t *testing.T, f *fixture) *nativeRelay {
	t.Helper()
	connection, err := grpc.NewClient(f.native, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDisableRetry())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.WaitForHandlers(true))
	r := &nativeRelay{target: handlers.NewHandlerServiceClient(connection)}
	handlers.RegisterHandlerServiceServer(server, r)
	stats.RegisterStatsServiceServer(server, relayStats{target: stats.NewStatsServiceClient(connection)})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	f.config.Instances[0].Xray.API.Address = listener.Addr().String()
	return r
}
func (r *nativeRelay) ListOutbounds(ctx context.Context, request *handlers.ListOutboundsRequest) (*handlers.ListOutboundsResponse, error) {
	r.lists.Add(1)
	return r.target.ListOutbounds(ctx, request, grpc.MaxCallRecvMsgSize(32<<20))
}
func (r *nativeRelay) AddOutbound(ctx context.Context, request *handlers.AddOutboundRequest) (*handlers.AddOutboundResponse, error) {
	r.adds.Add(1)
	response, err := r.target.AddOutbound(ctx, request)
	if err == nil && r.loseAdd.Load() {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	return response, err
}
func (r *nativeRelay) RemoveOutbound(ctx context.Context, request *handlers.RemoveOutboundRequest) (*handlers.RemoveOutboundResponse, error) {
	r.removes.Add(1)
	return r.target.RemoveOutbound(ctx, request)
}

type relayStats struct {
	stats.UnimplementedStatsServiceServer
	target stats.StatsServiceClient
}

func (r relayStats) GetSysStats(ctx context.Context, request *stats.SysStatsRequest) (*stats.SysStatsResponse, error) {
	return r.target.GetSysStats(ctx, request)
}
