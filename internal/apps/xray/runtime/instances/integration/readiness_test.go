//go:build integration && linux

package integration_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNativeFailedReadinessPreservesIdentityAndLogsOwnsBoundedOutput(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	e := recoverableEngine(t, "degraded")
	// Actual Xray serves traffic. The configured readiness endpoint is deliberately unavailable.
	e.settings.Xray.API.Address = availableAddress(t)
	config := nodeConfig(t, pki, broker, "all", e)
	_, c, outputs := startObservedNode(t, pki, config)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "instance-traffic-proof") }))
	defer target.Close()
	deadline := time.Now().Add(3 * time.Second)
	for traffic(e.proxy, target.URL) != nil {
		if time.Now().After(deadline) {
			t.Fatal("native traffic did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(outputs) != 1 || outputs[0].ID != e.settings.ID {
		t.Fatal("Logs output binding absent", outputs)
	}
	source := outputs[0].Source
	for _, transport := range []string{"http", "grpc", "nats"} {
		before := childRecord(t, e)
		probe, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		err := c.ready(probe, transport, e.settings.ID)
		cancel()
		if err == nil {
			t.Fatal("unavailable endpoint reported ready")
		}
		q := map[string]any{"execution_id": before.ExecutionID, "max_attempts": uint32(1), "replace_unready": false}
		op, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		r, err := c.extended(op, transport, "repair", e.settings.ID, q)
		cancel()
		if err == nil {
			t.Fatal("failed readiness repaired without consent")
		}
		if childRecord(t, e).ExecutionID != before.ExecutionID {
			t.Fatal("failed probe restarted engine")
		}
		if detail, ok := r["repair"].(map[string]any); ok {
			r = detail
		}
		if r["attempts"] != float64(0) || r["control"].(map[string]any)["effect"] != "not_applied" {
			t.Fatal(r)
		}
		q["replace_unready"] = true
		op, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		r, err = c.extended(op, transport, "repair", e.settings.ID, q)
		cancel()
		if err == nil {
			t.Fatal("unready replacement reported ready")
		}
		if detail, ok := r["repair"].(map[string]any); ok {
			r = detail
		}
		if r["attempts"] != float64(1) {
			t.Fatal("attempt bound lost", r)
		}
		after := childRecord(t, e)
		if after.ExecutionID == before.ExecutionID || after.Generation != before.Generation+1 {
			t.Fatal("replacement identity", after)
		}
		if _, err := source.ReadOutput(context.Background(), domain.OutputRequest{ExecutionID: before.ExecutionID}); !errors.Is(err, domain.ErrExecutionChanged) {
			t.Fatal("Logs crossed execution", err)
		}
		page, err := source.ReadOutput(context.Background(), domain.OutputRequest{ExecutionID: after.ExecutionID, Limit: domain.MaxOutputRead})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Data) == 0 || !strings.Contains(string(page.Data), "Xray") || page.Generation != after.Generation || page.NextOffset > domain.MaxOutputRead || page.ObservedAt.IsZero() {
			t.Fatal("raw native console unavailable", page)
		}
		if err := traffic(e.proxy, target.URL); err != nil {
			t.Fatal("failed readiness destroyed traffic", err)
		}
	}
}
