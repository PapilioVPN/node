//go:build integration && linux

package integration_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/store"
)

func TestNativeStageActivateRollbackAndCorruptInput(t *testing.T) {
	f := setup(t)
	old := f.selection(t)
	execution := f.runtime.Observe().ExecutionID
	for _, kind := range []string{"digest", "binary", "geodata"} {
		t.Run(kind, func(t *testing.T) {
			c := candidate(t, "PROOF", "candidate.test")
			switch kind {
			case "digest":
				c.Binary.SHA256 = c.GeoIP.SHA256
			case "binary":
				c.Binary = blob([]byte("invalid executable"))
			case "geodata":
				c.GeoSite = blob([]byte{255, 255})
			}
			if _, err := f.service.Stage(context.Background(), "one", c); err == nil {
				t.Fatal("corrupt input admitted")
			}
			if now := f.selection(t); now.State != old.State || f.runtime.Observe().ExecutionID != execution {
				t.Fatal("corrupt candidate changed execution or selection")
			}
			f.traffic(t)
		})
	}
	next, err := f.service.Stage(context.Background(), "one", candidate(t, "PROOF", "candidate.test"))
	if err != nil {
		t.Fatal(err)
	}
	if f.selection(t).State != old.State {
		t.Fatal("stage became active")
	}
	result, err := f.service.Activate(context.Background(), "one", f.activation(t, next.ID))
	if err != nil || result.Outcome != "completed" || result.State.Active != next.ID {
		t.Fatal(result, err)
	}
	f.traffic(t)
	// Explicit rollback also repairs a committed release whose execution stopped.
	if err := f.runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err = f.service.Rollback(context.Background(), "one", f.activation(t, ""))
	if err != nil || result.Outcome != "completed" || result.State.Active != old.State.Active || result.State.Previous != next.ID {
		t.Fatal(result, err)
	}
	f.traffic(t)
	files, err := os.ReadDir(filepath.Join(f.root, "releases"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatal("failed preparation leaked files", len(files))
	}
}
func TestNativeFailedReadinessRestoresWorkingAssets(t *testing.T) {
	f := setup(t)
	old := f.selection(t)
	next, err := f.service.Stage(context.Background(), "one", candidate(t, "OTHER", "incompatible.test"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.service.Activate(context.Background(), "one", f.activation(t, next.ID))
	if err == nil || result.Outcome != "restored" || !result.Ready || result.State.Active != old.State.Active || result.State.Pending != "" {
		t.Fatal(result, err)
	}
	f.traffic(t)
}
func TestNativeInterruptedTransitionPersistsRecoveryAcrossReopen(t *testing.T) {
	f := setup(t)
	old := f.selection(t)
	next, err := f.service.Stage(context.Background(), "one", candidate(t, "PROOF", "interrupted.test"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := f.repo.Begin(context.Background(), old.State.Revision, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.Select(context.Background(), next.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	await(t, f.runtime)
	// Deliberately leave durable intent and a running replacement without Finish.
	// Reopen the filesystem owner as after an interrupted Node operation.
	if err = f.service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.Close(); err != nil {
		t.Fatal(err)
	}
	f.repo, err = store.Open(f.root, "one")
	if err != nil {
		t.Fatal(err)
	}
	f.bind(t)
	if _, err = f.service.Activate(context.Background(), "one", f.activation(t, next.ID)); !errors.Is(err, domain.ErrRecovery) {
		t.Fatal("pending intent was ignored", err)
	}
	result, err := f.service.Recover(context.Background(), "one", domain.Activation{ExpectedRevision: state.Revision, ExecutionID: f.runtime.Observe().ExecutionID})
	if err != nil || result.Outcome != "restored" || result.State.Active != old.State.Active || result.State.Pending != "" {
		t.Fatal(result, err)
	}
	f.traffic(t)
}
