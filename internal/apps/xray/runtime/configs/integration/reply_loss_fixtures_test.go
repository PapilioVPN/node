//go:build integration && linux

package integration_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	routing "github.com/xtls/xray-core/app/router/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// This relay forwards to standalone pinned Xray and drops only the ACK after
// the actual native routing mutation has completed. It is not an engine mock.
type configRelay struct {
	routing.UnimplementedRoutingServiceServer
	target routing.RoutingServiceClient
	adds   atomic.Int64
	lose   atomic.Bool
}

func newConfigRelay(t *testing.T, f fixture, native string) *configRelay {
	t.Helper()
	connection, err := grpc.NewClient(native, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDisableRetry())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.WaitForHandlers(true))
	relay := &configRelay{target: routing.NewRoutingServiceClient(connection)}
	routing.RegisterRoutingServiceServer(server, relay)
	stats.RegisterStatsServiceServer(server, configRelayStats{target: stats.NewStatsServiceClient(connection)})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	f.config.Instances[0].Xray.API.Address = listener.Addr().String()
	return relay
}
func (r *configRelay) AddRule(ctx context.Context, request *routing.AddRuleRequest) (*routing.AddRuleResponse, error) {
	r.adds.Add(1)
	response, err := r.target.AddRule(ctx, request)
	if err == nil && r.lose.Load() {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	return response, err
}

type configRelayStats struct {
	stats.UnimplementedStatsServiceServer
	target stats.StatsServiceClient
}

func (r configRelayStats) GetSysStats(ctx context.Context, request *stats.SysStatsRequest) (*stats.SysStatsResponse, error) {
	return r.target.GetSysStats(ctx, request)
}
