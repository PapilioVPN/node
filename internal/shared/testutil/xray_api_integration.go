//go:build integration

package testutil

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
)

// Controlled SDK fixtures verify RPC/channel contracts, not real engine behavior.
type XrayRuntimeServer struct {
	native.UnimplementedStatsServiceServer
	Read func(context.Context) (*native.SysStatsResponse, error)
}

func (s XrayRuntimeServer) GetSysStats(ctx context.Context, _ *native.SysStatsRequest) (*native.SysStatsResponse, error) {
	return s.Read(ctx)
}

type XrayAPICounts struct {
	Opened atomic.Int32
	RPCs   atomic.Int64
}

func (s *XrayAPICounts) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	s.RPCs.Add(1)
	return ctx
}
func (*XrayAPICounts) HandleRPC(context.Context, stats.RPCStats) {}
func (*XrayAPICounts) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (s *XrayAPICounts) HandleConn(_ context.Context, event stats.ConnStats) {
	if _, ok := event.(*stats.ConnBegin); ok {
		s.Opened.Add(1)
	}
}

func XrayStatsAPI(t testing.TB, service native.StatsServiceServer) (string, *XrayAPICounts) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	counts := &XrayAPICounts{}
	server := grpc.NewServer(grpc.StatsHandler(counts))
	if service != nil {
		native.RegisterStatsServiceServer(server, service)
	}
	finished := make(chan struct{})
	go func() { defer close(finished); _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); server.GracefulStop(); <-finished })
	return listener.Addr().String(), counts
}
