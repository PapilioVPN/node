//go:build integration && linux

package integration_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type fixtureOptions struct {
	extraOutbounds    int
	trailingOutbounds bool
	relay             bool
}

// nativeRelay instruments calls forwarded to the real standalone engine. Faults
// affect acknowledgements after the real swap, or reject before forwarding.
type nativeRelay struct {
	paused  chan struct{}
	release chan struct{}
	stats.UnimplementedStatsServiceServer
	target                  stats.StatsServiceClient
	gets, scans, resets     atomic.Int64
	failAt, loseAt, pauseAt atomic.Int64
}

func newNativeRelay(t *testing.T, target string) (string, *nativeRelay) {
	t.Helper()
	connection, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDisableRetry())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &nativeRelay{target: stats.NewStatsServiceClient(connection), paused: make(chan struct{}), release: make(chan struct{})}
	server := grpc.NewServer(grpc.WaitForHandlers(true))
	stats.RegisterStatsServiceServer(server, service)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return listener.Addr().String(), service
}
func (r *nativeRelay) GetStats(ctx context.Context, request *stats.GetStatsRequest) (*stats.GetStatsResponse, error) {
	var index int64
	if request.Reset_ {
		index = r.resets.Add(1)
		if index == r.failAt.Load() {
			return nil, status.Error(codes.Unimplemented, "injected before forwarding")
		}
	} else {
		r.gets.Add(1)
	}
	result, err := r.target.GetStats(ctx, request)
	if err == nil && request.Reset_ && index == r.pauseAt.Load() {
		close(r.paused)
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	if err == nil && request.Reset_ && index == r.loseAt.Load() {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	return result, err
}
func (r *nativeRelay) QueryStats(ctx context.Context, request *stats.QueryStatsRequest) (*stats.QueryStatsResponse, error) {
	r.scans.Add(1)
	return r.target.QueryStats(ctx, request, grpc.MaxCallRecvMsgSize(32<<20))
}
func (r *nativeRelay) GetSysStats(ctx context.Context, request *stats.SysStatsRequest) (*stats.SysStatsResponse, error) {
	return r.target.GetSysStats(ctx, request)
}
