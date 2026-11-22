//go:build integration && linux

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/protobuf/proto"
)

func fullInput(t *testing.T, f *fullFixture, id string, users [][]byte) domain.Request {
	t.Helper()
	revision, err := f.service.Revision(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	return domain.Request{InstanceID: "one", OperationID: id, Expected: revision, Mode: domain.Full, Selection: []string{"selected"}, Sets: []accounts.InboundUserSet{{InboundTag: "selected", Accounts: users}}}
}
func TestNativeFullOneCompileAndDurableReplacement(t *testing.T) {
	for _, n := range []int{1, 50, 51, 1000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := withFull(t, newFixture(t))
			ctx, cancel := deadline(t)
			defer cancel()
			users := make([][]byte, n)
			for i := range users {
				users[i] = nativeUser(i)
			}
			r := fullInput(t, f, "full", users)
			if n == 1000 {
				transfers := services.NewTransfers()
				defer transfers.Close()
				header := r
				header.Sets = nil
				size := 0
				for _, raw := range users {
					size += len(raw)
				}
				transfer, err := transfers.Begin(ctx, header, n, size)
				if err != nil {
					t.Fatal(err)
				}
				for start, sequence := 0, uint32(0); start < n; start, sequence = start+domain.MaxChunkItems, sequence+1 {
					end := min(start+domain.MaxChunkItems, n)
					chunk := services.Chunk{Sequence: sequence, Final: end == n}
					for _, raw := range users[start:end] {
						chunk.Additions = append(chunk.Additions, accounts.Addition{InboundTag: "selected", Configuration: raw})
					}
					if _, err := transfers.Append(ctx, "one", transfer.ID, chunk); err != nil {
						t.Fatal(err)
					}
				}
				if f.compiler.calls != 0 {
					t.Fatal("chunk transfer invoked compiler")
				}
				if traffic(f.address, f.target, 0) == nil {
					t.Fatal("chunk transfer made staged account live")
				}
				r, err = transfers.Finish(ctx, "one", transfer.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			p, err := f.service.Preview(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if f.compiler.calls != 1 || p.ApplyMode != "restart" || p.ConfigOperationID == "" {
				t.Fatal(p, f.compiler.calls)
			}
			result, err := f.service.Apply(ctx, "one", p.PlanID, p.Revision)
			if err != nil || result.Receipt.Effect != accounts.Completed || !result.Receipt.Durable || !result.Receipt.Ready {
				t.Fatal(result, err)
			}
			if err := traffic(f.address, f.target, 0); err != nil {
				t.Fatal(err)
			}
			if err := traffic(f.excluded, f.target, 4095); err != nil {
				t.Fatal("excluded inbound changed", err)
			}
			if _, err = f.service.Apply(ctx, "one", p.PlanID, p.Revision); err != nil || f.compiler.calls != 1 {
				t.Fatal(err, f.compiler.calls)
			}
			stored, err := f.store.Read(ctx)
			if err != nil || stored.Metadata.Revision != 1 || !stored.Metadata.Durable {
				t.Fatal(stored.Metadata, err)
			}
			// Engine restart consumes Configs' private durable candidate, not a
			// volatile user cache or a replay of account mutations.
			if _, err := f.instances.Restart(ctx, "one"); err != nil {
				t.Fatal(err)
			}
			waitAuth(t, ctx, f, 0)
			r = fullInput(t, f, "clear", [][]byte{})
			p, err = f.service.Preview(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.service.Apply(ctx, "one", p.PlanID, p.Revision); err != nil {
				t.Fatal(err)
			}
			if err := traffic(f.address, f.target, 0); err == nil {
				t.Fatal("explicit empty full membership retained credential")
			}
			if err := traffic(f.excluded, f.target, 4095); err != nil {
				t.Fatal("clear changed excluded inbound", err)
			}
			t.Logf("native full n=%d compile_per_preview=1 live_account_mutations=0 durable=true", n)
		})
	}
}
func TestNativeFullFailedExecutionRecoveryAndFreshSyncOwner(t *testing.T) {
	f := withFull(t, newFixture(t))
	ctx, cancel := deadline(t)
	defer cancel()
	p, err := f.service.Preview(ctx, fullInput(t, f, "good", [][]byte{nativeUser(0)}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Apply(ctx, "one", p.PlanID, p.Revision); err != nil {
		t.Fatal(err)
	}
	badAccount, _ := proto.Marshal(&vless.Account{Id: "invalid-native-uuid-that-exceeds-thirty-six-characters"})
	bad, _ := proto.Marshal(&protocol.User{Email: "member-0", Account: &serial.TypedMessage{Type: "xray.proxy.vless.Account", Value: badAccount}})
	p, err = f.service.Preview(ctx, fullInput(t, f, "bad", [][]byte{bad}))
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.service.Apply(ctx, "one", p.PlanID, p.Revision)
	if err == nil || !r.Receipt.Durable || !r.Receipt.RequiresRecovery || r.Receipt.Ready {
		t.Fatal(r, err)
	}
	// Reconstruct all owners: epochs, reviews, channels and receipts are gone.
	// The opaque Configs identity, disclosed BEFORE apply, still reads and
	// restores the durable operation without any new account mutation.
	f = restartOwners(t, f)
	fresh := f.service
	status, err := fresh.Status(ctx, "one", p.ConfigOperationID, 0)
	if err != nil || !status.Receipt.RequiresRecovery {
		t.Fatal(status, err)
	}
	recovered, err := fresh.Recover(ctx, "one", p.ConfigOperationID, r.Receipt.Revision.Config)
	if err != nil || !recovered.Receipt.Ready || !recovered.Receipt.Durable || recovered.Receipt.RequiresRecovery {
		t.Fatal(recovered, err)
	}
	waitAuth(t, ctx, f, 0)
	before, _ := f.instances.Status(ctx, "one")
	if _, err = fresh.Recover(ctx, "one", p.ConfigOperationID, r.Receipt.Revision.Config); err != nil {
		t.Fatal(err)
	}
	after, _ := f.instances.Status(ctx, "one")
	if before.Execution.Generation != after.Execution.Generation {
		t.Fatal("recovery replay restarted execution")
	}
	if _, err = fresh.Recover(ctx, "one", p.ConfigOperationID, r.Receipt.Revision.Config+1); !errors.Is(err, configs.ErrConflict) {
		t.Fatalf("changed recovery revision: %v", err)
	}
}
func TestFullRejectsSourceEnvironmentBeforePublication(t *testing.T) {
	f := withFull(t, newFixture(t))
	ctx, cancel := deadline(t)
	defer cancel()
	source, err := os.ReadFile(f.baseline)
	if err != nil {
		t.Fatal(err)
	}
	source = append([]byte(`{"env":{"XRAY_LOCATION_ASSET":"/explicit"},`), source[1:]...)
	if err = os.WriteFile(f.baseline, source, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Preview(ctx, fullInput(t, f, "env", [][]byte{})); !errors.Is(err, configs.ErrUnsupported) {
		t.Fatal(err)
	}
	metadata, _ := f.store.Metadata(ctx)
	if metadata.Revision != 0 || f.compiler.calls != 0 {
		t.Fatal(metadata, f.compiler.calls)
	}
}

func TestNativeFullAnonymousCredentialAndClear(t *testing.T) {
	f := withFull(t, newFixture(t))
	ctx, cancel := deadline(t)
	defer cancel()
	var anonymous protocol.User
	if err := proto.Unmarshal(nativeUser(0), &anonymous); err != nil {
		t.Fatal(err)
	}
	anonymous.Email = ""
	raw, err := proto.Marshal(&anonymous)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.service.Preview(ctx, fullInput(t, f, "anonymous", [][]byte{raw}))
	if err != nil {
		t.Fatal(err)
	}
	if p.ObservationBasis != "native_manager_readback" {
		t.Fatal(p)
	}
	if _, err = f.service.Apply(ctx, "one", p.PlanID, p.Revision); err != nil {
		t.Fatal(err)
	}
	if err := traffic(f.address, f.target, 0); err != nil {
		t.Fatal("anonymous full membership", err)
	}
	observed, err := f.accounts.Count(ctx, "one", "selected")
	if err != nil || observed.Count != 0 {
		t.Fatal("pinned VLESS email index behavior", observed, err)
	}
	p, err = f.service.Preview(ctx, fullInput(t, f, "clear-anonymous", [][]byte{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Apply(ctx, "one", p.PlanID, p.Revision); err != nil {
		t.Fatal(err)
	}
	if traffic(f.address, f.target, 0) == nil {
		t.Fatal("clearing selected full membership retained anonymous credential")
	}
}

func waitAuth(t *testing.T, ctx context.Context, f *fullFixture, id int) {
	t.Helper()
	for {
		if err := traffic(f.address, f.target, id); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("durable account did not authenticate after restart")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
