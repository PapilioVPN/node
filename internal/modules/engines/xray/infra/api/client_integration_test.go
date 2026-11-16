//go:build integration

package api_test

import (
	"context"
	"errors"
	"math"
	"net"
	"sync/atomic"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	routingxray "github.com/PapilioVPN/node/internal/modules/core/routing/infra/xray/api"
	trafficxray "github.com/PapilioVPN/node/internal/modules/core/traffic/infra/xray/api"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayapi "github.com/PapilioVPN/node/internal/modules/engines/xray/infra/api"
	nativeapi "github.com/PapilioVPN/node/internal/platform/engines/xray/api"
)

// Controlled SDK servers exercise native channel contracts, not Xray behavior.
type runtimeServer struct {
	native.UnimplementedStatsServiceServer
	read func(context.Context) (*native.SysStatsResponse, error)
}

func (s runtimeServer) GetSysStats(ctx context.Context, _ *native.SysStatsRequest) (*native.SysStatsResponse, error) {
	return s.read(ctx)
}

type connectionCounts struct {
	opened atomic.Int32
	rpcs   atomic.Int64
}

func (s *connectionCounts) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	s.rpcs.Add(1)
	return ctx
}
func (*connectionCounts) HandleRPC(context.Context, stats.RPCStats) {}
func (*connectionCounts) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (s *connectionCounts) HandleConn(_ context.Context, event stats.ConnStats) {
	if _, ok := event.(*stats.ConnBegin); ok {
		s.opened.Add(1)
	}
}

func apiServer(t *testing.T, service native.StatsServiceServer) (string, *connectionCounts) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	counts := &connectionCounts{}
	server := grpc.NewServer(grpc.StatsHandler(counts))
	if service != nil {
		native.RegisterStatsServiceServer(server, service)
	}
	finished := make(chan struct{})
	go func() { defer close(finished); _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); server.GracefulStop(); <-finished })
	return listener.Addr().String(), counts
}

// All adapters borrow the same instance resource; only Client owns cleanup.
type apiClientSet struct {
	*trafficxray.Counters
	*nativeapi.Client
	*xrayapi.Adapter
	*routingxray.Router
}

func apiClient(t *testing.T, address string, timeout time.Duration, limit int) *apiClientSet {
	t.Helper()
	client, err := nativeapi.NewClient(nativeapi.Config{Network: "tcp", Address: address, RequestTimeout: timeout, MaxReceiveBytes: limit, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return &apiClientSet{Counters: &trafficxray.Counters{API: client}, Client: client, Adapter: &xrayapi.Adapter{API: client}, Router: &routingxray.Router{API: client}}
}

func TestAPIReusesNativeChannelAndPreservesUnsignedCounters(t *testing.T) {
	var calls atomic.Int32
	address, connections := apiServer(t, runtimeServer{read: func(context.Context) (*native.SysStatsResponse, error) {
		calls.Add(1)
		return &native.SysStatsResponse{NumGoroutine: math.MaxUint32, NumGC: math.MaxUint32, Alloc: math.MaxUint64, TotalAlloc: math.MaxUint64, Sys: math.MaxUint64, Mallocs: math.MaxUint64, Frees: math.MaxUint64, LiveObjects: math.MaxUint64, PauseTotalNs: math.MaxUint64, Uptime: math.MaxUint32}, nil
	}})
	client := apiClient(t, address, time.Second, 1024)
	before := time.Now()
	for range 3 {
		info, err := client.ReadRuntimeInfo(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if info.Goroutines != math.MaxUint32 || info.GCCount != math.MaxUint32 || info.AllocatedBytes != math.MaxUint64 || info.TotalAllocatedBytes != math.MaxUint64 || info.SysBytes != math.MaxUint64 || info.Mallocs != math.MaxUint64 || info.Frees != math.MaxUint64 || info.LiveObjects != math.MaxUint64 || info.TotalGCPauseNS != math.MaxUint64 || info.UptimeSeconds != math.MaxUint32 || info.ObservedAt.Before(before) || info.ObservedAt.After(time.Now()) {
			t.Fatalf("unsigned native facts changed: %+v", info)
		}
	}
	if calls.Load() != 3 || connections.opened.Load() != 1 {
		t.Fatalf("native calls=%d, connections=%d", calls.Load(), connections.opened.Load())
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := client.ReadRuntimeInfo(t.Context()); err == nil || info != (domain.RuntimeInfo{}) {
		t.Fatalf("closed channel accepted a read: %+v, %v", info, err)
	}
	if calls.Load() != 3 {
		t.Fatal("read after Close reached native server")
	}
}

func TestAPINativeMethodAndResponseLimits(t *testing.T) {
	missing, _ := apiServer(t, nil)
	if _, err := apiClient(t, missing, time.Second, 1024).ReadRuntimeInfo(t.Context()); !errors.Is(err, domain.ErrRuntimeInfoUnsupported) || status.Code(err) != codes.Unimplemented {
		t.Fatalf("missing method cause: %v", err)
	}
	address, _ := apiServer(t, runtimeServer{read: func(context.Context) (*native.SysStatsResponse, error) {
		return &native.SysStatsResponse{Alloc: math.MaxUint64}, nil
	}})
	if info, err := apiClient(t, address, time.Second, 1).ReadRuntimeInfo(t.Context()); !errors.Is(err, domain.ErrAPIResourceLimit) || status.Code(err) != codes.ResourceExhausted || info != (domain.RuntimeInfo{}) {
		t.Fatalf("native response limit: %+v, %v", info, err)
	}
}

func TestAPIAdmissionCancellationDeadlineAndClose(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout", "close"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{}, 1)
			address, _ := apiServer(t, runtimeServer{read: func(ctx context.Context) (*native.SysStatsResponse, error) {
				started <- struct{}{}
				<-ctx.Done()
				return nil, status.FromContextError(ctx.Err()).Err()
			}})
			client := apiClient(t, address, time.Second, 1024)
			ctx, cancel := context.WithCancel(t.Context())
			finished := make(chan struct{})
			var result domain.RuntimeInfo
			var callError error
			go func() { result, callError = client.ReadRuntimeInfo(ctx); close(finished) }()
			defer func() { cancel(); _ = client.Close(); <-finished }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("native call was not admitted")
			}
			if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, domain.ErrAPIBusy) {
				t.Fatalf("saturation queued another read: %v", err)
			}
			switch mode {
			case "cancel":
				cancel()
			case "close":
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
			}
			<-finished
			if result != (domain.RuntimeInfo{}) || callError == nil {
				t.Fatalf("interrupted read became facts: %+v, %v", result, callError)
			}
			if mode == "cancel" && !errors.Is(callError, context.Canceled) || mode == "timeout" && !errors.Is(callError, context.DeadlineExceeded) {
				t.Fatalf("interruption cause changed: %v", callError)
			}
			if mode != "close" {
				next, stop := context.WithTimeout(t.Context(), 100*time.Millisecond)
				_, err := client.ReadRuntimeInfo(next)
				stop()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("capacity was not released or shorter caller deadline lost: %v", err)
				}
			}
			canceled, stop := context.WithCancel(t.Context())
			stop()
			if _, err := client.ReadRuntimeInfo(canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled admission: %v", err)
			}
		})
	}
}
