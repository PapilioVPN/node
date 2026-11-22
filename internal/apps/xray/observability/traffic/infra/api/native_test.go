//go:build integration

package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestPinnedNativeTargetedCallGrowthAndReadOnlyValues(t *testing.T) {
	for _, network := range []string{"tcp", "unix"} {
		t.Run(network, func(t *testing.T) {
			manager, client := nativeFixture(t, network)
			reader := New(client)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, scope := range []domain.Scope{domain.User, domain.Inbound, domain.Outbound} {
				for _, direction := range []domain.Direction{domain.Uplink, domain.Downlink} {
					name := string(scope) + ">>> A>>>B/نام >>>traffic>>>" + string(direction)
					counter, err := manager.RegisterCounter(name)
					if err != nil {
						t.Fatal(err)
					}
					for _, value := range []int64{0, math.MinInt64, math.MaxInt64} {
						counter.Set(value)
						before := manager.gets.Load()
						got, err := reader.Read(ctx, domain.Selection{Scope: scope, Name: " A>>>B/نام ", Direction: direction})
						if err != nil || got != value || counter.Value() != value || manager.gets.Load()-before != 1 || manager.visits.Load() != 0 {
							t.Fatalf("targeted read bytes=%d error=%v GetCounter=%d VisitCounters=%d", got, err, manager.gets.Load()-before, manager.visits.Load())
						}
					}
				}
			}
			previous := 0
			for _, size := range []int{0, 1, 50, 51, 1000} {
				for i := previous; i < size; i++ {
					if _, err := manager.RegisterCounter(fmt.Sprintf("user>>>unrelated-%d>>>traffic>>>uplink", i)); err != nil {
						t.Fatal(err)
					}
				}
				previous = size
				before := manager.gets.Load()
				_, err := reader.Read(ctx, domain.Selection{Scope: domain.User, Name: " A>>>B/نام ", Direction: domain.Uplink})
				if err != nil || manager.gets.Load()-before != 1 || manager.visits.Load() != 0 {
					t.Fatal("inventory size changed read path", size, err)
				}
				t.Logf("unrelated counters=%d GetStats/GetCounter=1 collection scans=0", size)
			}
			before := manager.gets.Load()
			if _, err := reader.Read(ctx, domain.Selection{Scope: domain.User, Name: "absent", Direction: domain.Downlink}); !errors.Is(err, domain.ErrCounterNotFound) || manager.gets.Load()-before != 1 {
				t.Fatal("missing counter semantics", err)
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			before = manager.gets.Load()
			if _, err := reader.Read(ctx, domain.Selection{Scope: domain.User, Name: "absent", Direction: domain.Downlink}); !errors.Is(err, domain.ErrClosed) || manager.gets.Load() != before {
				t.Fatal("closed resource dispatched", err)
			}
		})
	}
}

func TestNativeReplyLimitAndCancellationReturnNoSuccessfulFacts(t *testing.T) {
	selection := domain.Selection{Scope: domain.User, Name: "a", Direction: domain.Uplink}
	t.Run("receive bound", func(t *testing.T) {
		client := serveNative(t, "tcp", responseServer{read: func(_ context.Context, request *stats.GetStatsRequest) (*stats.GetStatsResponse, error) {
			result := &stats.GetStatsResponse{Stat: &stats.Stat{Name: request.Name, Value: 123}}
			unknown := protowire.AppendTag(nil, 100, protowire.BytesType)
			unknown = protowire.AppendBytes(unknown, make([]byte, 80<<10))
			result.ProtoReflect().SetUnknown(unknown)
			return result, nil
		}})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if value, err := New(client).Read(ctx, selection); value != 0 || !errors.Is(err, domain.ErrCapacity) {
			t.Fatalf("oversized reply value=%d error=%v", value, err)
		}
	})
	for _, mode := range []string{"deadline", "close"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			finished := make(chan struct{})
			client := serveNative(t, "tcp", responseServer{read: func(ctx context.Context, _ *stats.GetStatsRequest) (*stats.GetStatsResponse, error) {
				defer close(finished)
				close(entered)
				<-ctx.Done()
				return nil, ctx.Err()
			}})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if mode == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 100*time.Millisecond)
				defer stop()
			}
			result := make(chan error, 1)
			go func() {
				value, err := New(client).Read(ctx, selection)
				if value != 0 {
					t.Error("failed read retained value")
				}
				result <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("native handler not entered")
			}
			if mode == "close" {
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-result:
				if err == nil || (mode == "deadline" && !errors.Is(err, context.DeadlineExceeded)) {
					t.Fatal("native wait returned success or lost deadline", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("native read did not finish after deadline or close")
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("native handler did not finish")
			}
		})
	}
}
