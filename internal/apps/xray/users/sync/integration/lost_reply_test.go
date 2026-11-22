//go:build integration && linux

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNativeLostAcknowledgementFencesReplay(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	revision, err := f.service.Revision(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	r := domain.Request{InstanceID: "one", OperationID: "lost", Expected: revision, Mode: domain.Delta, Selection: []string{"selected"}, Sets: []accounts.InboundUserSet{{InboundTag: "selected", Accounts: [][]byte{nativeUser(0), nativeUser(1)}}}}
	p, err := f.service.Preview(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	relay, drop := testutil.LostReplies(t, f.native)
	connection, err := rpc.New(rpc.Config{Network: "tcp", Address: relay})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	f.calls.mu.Lock()
	f.calls.mutation = connection
	f.calls.mu.Unlock()
	f.calls.take()
	short, stop := context.WithTimeout(ctx, 300*time.Millisecond)
	receipt, err := f.service.Apply(short, "one", p.PlanID, p.Revision)
	stop()
	if err == nil || receipt.Receipt.Effect != accounts.Unknown || !receipt.Receipt.RequiresRecovery || len(receipt.Receipt.Steps) != 2 || receipt.Receipt.Steps[1].Attempted {
		t.Fatalf("lost ACK: %+v %v", receipt, err)
	}
	if err := traffic(f.address, f.target, 0); err != nil {
		t.Fatal("dispatched write did not actually authenticate", err)
	}
	if err := traffic(f.address, f.target, 1); err == nil {
		t.Fatal("unattempted account became live")
	}
	drop.Store(false)
	if _, err := f.service.Apply(ctx, "one", p.PlanID, p.Revision); err == nil {
		t.Fatal("replay must retain original uncertainty")
	}
	if got := f.calls.take(); got["AlterInbound"] != 1 {
		t.Fatal("replay resent native work", got)
	}
	if _, err := f.instances.Restart(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	for {
		if ready, err := f.instances.Check(ctx, "one"); err == nil && ready.APIState == "ready" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("replacement not ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := f.service.Refresh(ctx, "one", "lost"); err != nil {
		t.Fatal(err)
	}
	if err := traffic(f.address, f.target, 0); err == nil {
		t.Fatal("ephemeral uncertain account survived replacement")
	}
}
