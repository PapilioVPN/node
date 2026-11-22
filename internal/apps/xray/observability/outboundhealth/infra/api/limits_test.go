//go:build integration

package api

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	command "github.com/xtls/xray-core/app/observatory/command"
	"google.golang.org/grpc"
)

func TestRealGRPCReceiveDeadlineAndClosedBounds(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	native := &boundedServer{}
	server := grpc.NewServer()
	command.RegisterObservatoryServiceServer(server, native)
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); <-done })
	connection, err := rpc.New(rpc.Config{Network: "tcp", Address: listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	reader := New(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	native.size.Store(96 << 10)
	if got, err := reader.Read(ctx); err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	native.size.Store(domain.MaxNativeBytes)
	if got, err := reader.Read(ctx); !errors.Is(err, domain.ErrCapacity) || got != nil {
		t.Fatal(got, err)
	}
	native.size.Store(-1)
	short, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	if _, err := reader.Read(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	connection.Close()
	if _, err := reader.Read(ctx); !errors.Is(err, domain.ErrClosed) {
		t.Fatal(err)
	}
	if native.calls.Load() != 3 {
		t.Fatal(native.calls.Load())
	}
}
