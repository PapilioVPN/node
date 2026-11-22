//go:build integration

package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type runningStatus struct{}

func (runningStatus) Status(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 1}}, nil
}
func TestPinnedNativeCollectionPairResetCountsAndExactScopes(t *testing.T) {
	for _, network := range []string{"tcp", "unix"} {
		t.Run(network, func(t *testing.T) {
			manager, client := nativeFixture(t, network)
			adapter := New(client)
			s, _ := services.New([]services.Binding{{ID: "a", Reader: adapter, Lister: adapter, Resetter: adapter}}, runningStatus{})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for _, scope := range []domain.Scope{domain.User, domain.Inbound, domain.Outbound} {
				for _, direction := range []domain.Direction{domain.Uplink, domain.Downlink} {
					name := string(scope) + ">>> A>>>B >>>traffic>>>" + string(direction)
					c, err := manager.RegisterCounter(name)
					if err != nil {
						t.Fatal(err)
					}
					c.Set(math.MinInt64)
				}
			}
			collision, _ := manager.RegisterCounter("inbound>>>user>>>collision>>>traffic>>>uplink")
			collision.Set(123)
			for _, n := range []int{0, 1, 50, 51, 1000} {
				for i := 0; i < n; i++ {
					name := fmt.Sprintf("user>>>bulk-%04d-%s>>>traffic>>>uplink", i, strings.Repeat("x", 128))
					if manager.Manager.GetCounter(name) == nil {
						c, _ := manager.RegisterCounter(name)
						c.Set(int64(i))
					}
				}
				before := manager.visits.Load()
				got, err := adapter.List(ctx, domain.User)
				if err != nil || len(got) != n+2 || manager.visits.Load() != before+1 {
					t.Fatal(n, len(got), err)
				}
				beforeGets := manager.gets.Load()
				_, err = s.ReadPair(ctx, "a", domain.User, " A>>>B ")
				if err != nil || manager.gets.Load()-beforeGets != 2 {
					t.Fatal("pair scan or call growth", err)
				}
			}
			initial, _ := s.ResetStatus(ctx, "a", 0, 0)
			request := domain.ResetRequest{Epoch: initial.Epoch, OperationID: "scope-reset", Kind: domain.ResetScope, Selection: domain.Selection{Scope: domain.Inbound}, MaxCounters: 10}
			beforeVisits, beforeGets := manager.visits.Load(), manager.gets.Load()
			report, err := s.Reset(ctx, "a", request)
			if err != nil || report.Outcome != "completed" || report.Total != 3 || manager.visits.Load()-beforeVisits != 1 || manager.gets.Load()-beforeGets != 3 {
				t.Fatal(report, err)
			}
			if collision.Value() != 0 {
				t.Fatal("selected inbound collision not reset")
			}
			user, err := adapter.Read(ctx, domain.Selection{Scope: domain.User, Name: " A>>>B ", Direction: domain.Uplink})
			if err != nil || user != math.MinInt64 {
				t.Fatal("wrong scope reset", user, err)
			}
			value, err := adapter.Reset(ctx, domain.Selection{Scope: domain.User, Name: " A>>>B ", Direction: domain.Uplink})
			if err != nil || value != math.MinInt64 {
				t.Fatal(value, err)
			}
			value, err = adapter.Read(ctx, domain.Selection{Scope: domain.User, Name: " A>>>B ", Direction: domain.Uplink})
			if err != nil || value != 0 {
				t.Fatal(value, err)
			}
			if _, err = adapter.Reset(ctx, domain.Selection{Scope: domain.User, Name: "absent", Direction: domain.Uplink}); !errors.Is(err, domain.ErrCounterNotFound) || errors.Is(err, domain.ErrResetUnknown) {
				t.Fatal(err)
			}
		})
	}
}
