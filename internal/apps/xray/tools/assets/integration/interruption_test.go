//go:build integration && linux

package integration_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/store"
)

func TestNativeCanceledActivationRestoresAndJoins(t *testing.T) {
	f := setup(t)
	old := f.selection(t)
	next, err := f.service.Stage(context.Background(), "one", candidate(t, "PROOF", "canceled.test"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.afterStart = cancel
	result, err := f.service.Activate(ctx, "one", f.activation(t, next.ID))
	if !errors.Is(err, context.Canceled) || result.Outcome != "restored" || !result.Ready || result.State.Active != old.State.Active || result.State.Pending != "" {
		t.Fatal(result, err)
	}
	f.traffic(t)
}

// The helper dies after real filesystem publication, bypassing deferred cleanup.
// The test's existing Instances runtime remains solely owned by the parent.
func TestAssetCrashHelper(t *testing.T) {
	if os.Getenv("NODE_ASSET_CRASH_HELPER") != "1" {
		return
	}
	repo, err := store.Open(os.Getenv("NODE_ASSET_CRASH_ROOT"), "one")
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	next := os.Getenv("NODE_ASSET_CRASH_TARGET")
	if _, err = repo.Begin(context.Background(), state.Revision, next); err != nil {
		t.Fatal(err)
	}
	if err = repo.Select(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {}
}
func TestNativeKilledUpgradeWriterRecoversDurableIntent(t *testing.T) {
	f := setup(t)
	old := f.selection(t)
	next, err := f.service.Stage(context.Background(), "one", candidate(t, "PROOF", "killed.test"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = f.runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.Close(); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAssetCrashHelper$", "-test.count=1")
	child.Env = append(os.Environ(), "NODE_ASSET_CRASH_HELPER=1", "NODE_ASSET_CRASH_ROOT="+f.root, "NODE_ASSET_CRASH_TARGET="+next.ID)
	output, err := child.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("writer did not reach crash boundary: %v %s", err, output)
	}
	f.repo, err = store.Open(f.root, "one")
	if err != nil {
		t.Fatal(err)
	}
	f.bind(t)
	if err = f.runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	await(t, f.runtime)
	state, err := f.repo.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending != next.ID || state.Active != old.State.Active {
		t.Fatal("crash lost recovery authority", state)
	}
	result, err := f.service.Recover(context.Background(), "one", domain.Activation{ExpectedRevision: state.Revision, ExecutionID: f.runtime.Observe().ExecutionID})
	if err != nil || result.Outcome != "restored" || result.State.Active != old.State.Active {
		t.Fatal(result, err)
	}
	f.traffic(t)
}
