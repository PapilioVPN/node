package engine

import (
	"context"
	"strconv"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/native/scopeapi"
	native "github.com/xtls/xray-core/app/stats"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRealCounterRegistryScopeAndLimit(t *testing.T) {
	manager, err := native.NewManager(t.Context(), &native.Config{})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{
		"user>>>account>>>uplink>>>traffic>>>uplink",
		"user>>>account>>>uplink>>>traffic>>>downlink",
		"inbound>>>tag>>>user>>>account>>>traffic>>>uplink",
	}
	for i, name := range names {
		counter, err := manager.RegisterCounter(name)
		if err != nil {
			t.Fatal(err)
		}
		counter.Set(int64(i + 10))
	}
	server := &exactTraffic{manager: manager}
	if reply, err := server.ResetScope(t.Context(), &scopeapi.ScopeResetRequest{Scope: "user", MaxCounters: 1}); err != nil || !reply.GetLimitExceeded() || len(reply.GetCounters()) != 0 {
		t.Fatal("expected rejection before swaps", err)
	}
	for i, name := range names {
		if got := manager.GetCounter(name).Value(); got != int64(i+10) {
			t.Fatalf("over-limit operation changed %s to %d", name, got)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := server.ResetScope(ctx, &scopeapi.ScopeResetRequest{Scope: "user", MaxCounters: 2}); status.Code(err) != codes.Canceled {
		t.Fatal("expected cancellation before swaps", err)
	}
	result, err := server.ResetScope(t.Context(), &scopeapi.ScopeResetRequest{Scope: "user", MaxCounters: 2})
	if err != nil || len(result.GetCounters()) != 2 {
		t.Fatalf("unexpected scope reply %v, %v", result, err)
	}
	values := make(map[string]int64)
	for _, stat := range result.GetCounters() {
		direction := "uplink"
		if stat.GetDownlink() {
			direction = "downlink"
		}
		values["user>>>"+stat.GetName()+">>>traffic>>>"+direction] = stat.GetPreviousBytes()
	}
	for i, name := range names[:2] {
		if got := manager.GetCounter(name).Value(); got != 0 || values[name] != int64(i+10) {
			t.Fatalf("counter %s = %d, reported %d", name, got, values[name])
		}
	}
	if got := manager.GetCounter(names[2]).Value(); got != 12 {
		t.Fatalf("out-of-scope counter changed to %d", got)
	}
}

func TestLargeScopeUsesRequestedCompactResponse(t *testing.T) {
	manager, err := native.NewManager(t.Context(), &native.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 256 {
		counter, err := manager.RegisterCounter("user>>>account-" + strconv.Itoa(i) + ">>>traffic>>>downlink")
		if err != nil {
			t.Fatal(err)
		}
		counter.Set(-int64(i))
	}
	server := &exactTraffic{manager: manager}
	result, err := server.ResetScope(t.Context(), &scopeapi.ScopeResetRequest{Scope: "user", MaxCounters: 256, CompactReply: true})
	batch := result.GetCompactCounters()
	if err != nil || len(result.GetCounters()) != 0 || len(batch.GetNames()) != 256 || len(batch.GetDownlink()) != 256 || len(batch.GetPreviousBytes()) != 256 {
		t.Fatalf("unexpected compact scope response: %v", err)
	}
	for i, name := range batch.GetNames() {
		index, err := strconv.Atoi(name[len("account-"):])
		if err != nil || !batch.Downlink[i] || batch.PreviousBytes[i] != -int64(index) {
			t.Fatalf("incorrect acknowledgement for %q: %v", name, err)
		}
		if value := manager.GetCounter("user>>>" + name + ">>>traffic>>>downlink").Value(); value != 0 {
			t.Fatalf("%q after reset = %d", name, value)
		}
	}
}
