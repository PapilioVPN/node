//go:build integration

package api_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	observatory "github.com/xtls/xray-core/app/observatory/command"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	routing "github.com/xtls/xray-core/app/router/command"
	statsnative "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/platform/engines/xray/api"
)

type calls struct {
	connections atomic.Int32
	rpcs        atomic.Int32
}

func (c *calls) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (c *calls) HandleConn(_ context.Context, event stats.ConnStats) {
	if _, ok := event.(*stats.ConnBegin); ok {
		c.connections.Add(1)
	}
}
func (c *calls) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	c.rpcs.Add(1)
	return ctx
}
func (c *calls) HandleRPC(context.Context, stats.RPCStats) {}

// Controlled canonical SDK services return Unimplemented. Real engine effects
// remain covered by capability/bootstrap tests; this verifies the shared owner.
func TestNativeServicesShareOneChannelAcrossConcurrentFirstUseAndClose(t *testing.T) {
	for _, network := range []string{"tcp", "unix"} {
		t.Run(network, func(t *testing.T) {
			address := "127.0.0.1:0"
			if network == "unix" {
				address = filepath.Join(t.TempDir(), "api.sock")
			}
			listener, err := net.Listen(network, address)
			if err != nil {
				t.Fatal(err)
			}
			observed := &calls{}
			server := grpc.NewServer(grpc.StatsHandler(observed))
			statsnative.RegisterStatsServiceServer(server, &statsnative.UnimplementedStatsServiceServer{})
			handlers.RegisterHandlerServiceServer(server, &handlers.UnimplementedHandlerServiceServer{})
			routing.RegisterRoutingServiceServer(server, &routing.UnimplementedRoutingServiceServer{})
			observatory.RegisterObservatoryServiceServer(server, &observatory.UnimplementedObservatoryServiceServer{})
			served := make(chan struct{})
			go func() { defer close(served); _ = server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); server.GracefulStop(); <-served })
			owner, err := api.NewClient(api.Config{Network: network, Address: listener.Addr().String(), RequestTimeout: time.Second, MaxReceiveBytes: 4096, MaxConcurrent: 4})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = owner.Close() })
			requests := []func(context.Context) error{
				func(ctx context.Context) error {
					client, err := owner.StatsClient()
					if err != nil {
						return err
					}
					_, err = client.GetSysStats(ctx, &statsnative.SysStatsRequest{})
					return err
				},
				func(ctx context.Context) error {
					client, err := owner.HandlerClient()
					if err != nil {
						return err
					}
					_, err = client.GetInboundUsersCount(ctx, &handlers.GetInboundUserRequest{Tag: "in"})
					return err
				},
				func(ctx context.Context) error {
					client, err := owner.RoutingClient()
					if err != nil {
						return err
					}
					_, err = client.ListRule(ctx, &routing.ListRuleRequest{})
					return err
				},
				func(ctx context.Context) error {
					client, err := owner.ObservatoryClient()
					if err != nil {
						return err
					}
					_, err = client.GetOutboundStatus(ctx, &observatory.GetOutboundStatusRequest{})
					return err
				},
			}
			for range 2 {
				var joined sync.WaitGroup
				for _, request := range requests {
					joined.Go(func() {
						ctx, done, err := owner.Begin(t.Context())
						if err != nil {
							t.Error(err)
							return
						}
						defer done()
						if err := request(ctx); status.Code(err) != codes.Unimplemented {
							t.Errorf("native failure changed: %v", err)
						}
					})
				}
				joined.Wait()
			}
			if observed.connections.Load() != 1 || observed.rpcs.Load() != 8 {
				t.Fatalf("native sharing changed: connections=%d calls=%d", observed.connections.Load(), observed.rpcs.Load())
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			for _, request := range requests {
				if err := request(t.Context()); !errors.Is(err, grpc.ErrClientConnClosing) {
					t.Fatalf("closed resource admitted native work: %v", err)
				}
			}
			if observed.connections.Load() != 1 || observed.rpcs.Load() != 8 {
				t.Fatal("closed resource reopened/replayed native work")
			}
		})
	}
}
