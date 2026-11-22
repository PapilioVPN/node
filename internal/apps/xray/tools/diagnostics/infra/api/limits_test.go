//go:build integration

package api

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	reflection "google.golang.org/grpc/reflection/grpc_reflection_v1"
)

func TestNativeReflectionReceiveBoundAndJoinedStream(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &boundedServer{done: make(chan struct{})}
	server := grpc.NewServer()
	stats.RegisterStatsServiceServer(server, service)
	reflection.RegisterServerReflectionServer(server, service)
	joined := make(chan struct{})
	go func() { defer close(joined); _ = server.Serve(l) }()
	t.Cleanup(func() { server.Stop(); <-joined })
	conn, err := rpc.New(rpc.Config{Network: "tcp", Address: l.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, err := New(conn).Read(ctx)
	if err != nil || r.State != "available" || r.Runtime.State != "available" || r.Discovery.Reason != "resource_exhausted" {
		t.Fatal(r, err)
	}
	select {
	case <-service.done:
	case <-ctx.Done():
		t.Fatal("reflection stream was not canceled/joined")
	}
	if len(r.Capabilities) != 5 {
		t.Fatal(r)
	}
}
