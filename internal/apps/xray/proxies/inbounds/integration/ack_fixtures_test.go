//go:build integration && linux

package integration_test

import (
	"context"
	"sync/atomic"
	"testing"

	handlers "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// acknowledgementRelay forwards actual native calls. Its injected fault loses
// only a successful AddInbound response, after the real engine applies it. This
// is controlled transport fault injection, not a second engine implementation.
type acknowledgementRelay struct {
	handlers.UnimplementedHandlerServiceServer
	native handlers.HandlerServiceClient
	lose   atomic.Bool
}

func (r *acknowledgementRelay) ListInbounds(ctx context.Context, input *handlers.ListInboundsRequest) (*handlers.ListInboundsResponse, error) {
	return r.native.ListInbounds(ctx, input)
}
func (r *acknowledgementRelay) RemoveInbound(ctx context.Context, input *handlers.RemoveInboundRequest) (*handlers.RemoveInboundResponse, error) {
	return r.native.RemoveInbound(ctx, input)
}
func (r *acknowledgementRelay) AddInbound(ctx context.Context, input *handlers.AddInboundRequest) (*handlers.AddInboundResponse, error) {
	result, err := r.native.AddInbound(ctx, input)
	if err == nil && r.lose.Load() {
		return nil, status.Error(codes.Unavailable, "injected acknowledgement loss")
	}
	return result, err
}

type statsRelay struct {
	stats.UnimplementedStatsServiceServer
	native stats.StatsServiceClient
}

func (r statsRelay) GetSysStats(ctx context.Context, input *stats.SysStatsRequest) (*stats.SysStatsResponse, error) {
	return r.native.GetSysStats(ctx, input)
}
func replacementRelay(t *testing.T, target string) (string, *acknowledgementRelay) {
	t.Helper()
	connection, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	listener := reserveTCP(t)
	server := grpc.NewServer()
	relay := &acknowledgementRelay{native: handlers.NewHandlerServiceClient(connection)}
	handlers.RegisterHandlerServiceServer(server, relay)
	stats.RegisterStatsServiceServer(server, statsRelay{native: stats.NewStatsServiceClient(connection)})
	done := make(chan struct{})
	go func() { defer close(done); server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); <-done; connection.Close() })
	return listener.Addr().String(), relay
}
