//go:build integration

package api

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	nativestats "github.com/xtls/xray-core/app/stats"
	stats "github.com/xtls/xray-core/app/stats/command"
	featurestats "github.com/xtls/xray-core/features/stats"
	"google.golang.org/grpc"
)

// countedManager runs the actual pinned StatsService and counter implementation,
// with instrumentation of targeted versus collection access. It is not a child
// engine or a throughput benchmark.
type countedManager struct {
	*nativestats.Manager
	gets   atomic.Int64
	visits atomic.Int64
}

func (m *countedManager) GetCounter(name string) featurestats.Counter {
	m.gets.Add(1)
	return m.Manager.GetCounter(name)
}
func (m *countedManager) VisitCounters(visitor func(string, featurestats.Counter) bool) {
	m.visits.Add(1)
	m.Manager.VisitCounters(visitor)
}

func nativeFixture(t *testing.T, network string) (*countedManager, *rpc.Client) {
	t.Helper()
	manager, err := nativestats.NewManager(context.Background(), &nativestats.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m := &countedManager{Manager: manager}
	return m, serveNative(t, network, stats.NewStatsServer(m))
}

func serveNative(t *testing.T, network string, service stats.StatsServiceServer) *rpc.Client {
	t.Helper()
	address := "127.0.0.1:0"
	if network == "unix" {
		dir, err := os.MkdirTemp("", "traffic-native-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		address = filepath.Join(dir, "api.sock")
	}
	listener, err := net.Listen(network, address)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	stats.RegisterStatsServiceServer(server, service)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	client, err := rpc.New(rpc.Config{Network: network, Address: listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return client
}

type responseServer struct {
	stats.UnimplementedStatsServiceServer
	read func(context.Context, *stats.GetStatsRequest) (*stats.GetStatsResponse, error)
}

func (s responseServer) GetStats(ctx context.Context, request *stats.GetStatsRequest) (*stats.GetStatsResponse, error) {
	return s.read(ctx, request)
}
