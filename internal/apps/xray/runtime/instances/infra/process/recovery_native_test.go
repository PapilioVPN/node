//go:build integration && linux

package process

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"golang.org/x/sys/unix"
)

func TestReattachIdentityPendingIntentAndBoundedOutput(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "published", true: "fork_intent"}[pending], func(t *testing.T) {
			p := fixtureProcess(t, "normal")
			p.config.ID = "test"
			p.config.RecoveryDirectory = t.TempDir()
			os.Chmod(p.config.RecoveryDirectory, 0700)
			if err := p.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			before := p.Observe()
			waitFor(t, func() bool { _, err := os.Stat(p.config.Configuration + ".ready"); return err == nil })
			if pending {
				r := p.records.value
				r.PID = 0
				r.StartTicks = 0
				r.Pending = true
				if err := p.records.write(r); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.SetRetention(context.Background(), before.ExecutionID, true); err != nil {
				t.Fatal(err)
			}
			if err := p.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			q, err := New(p.config)
			if err != nil {
				t.Fatal(err)
			}
			defer q.Close(context.Background())
			if err := q.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			after := q.Observe()
			if after.ExecutionID != before.ExecutionID || after.Generation != before.Generation || !after.Reattached || !after.IdentityVerified {
				t.Fatalf("lost execution: %+v", after)
			}
			if _, err := q.ReadOutput(context.Background(), domain.OutputRequest{Limit: domain.MaxOutputRead + 1}); !errors.Is(err, domain.ErrInvalidArgument) {
				t.Fatal("output bound", err)
			}
			if err := q.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if q.Observe().ExitCode != nil {
				t.Fatal("adoption invented an exit code")
			}
		})
	}
}
func TestRecoveryRejectsChangedIdentityAndExclusiveOwner(t *testing.T) {
	p := fixtureProcess(t, "normal")
	p.config.ID = "test"
	p.config.RecoveryDirectory = t.TempDir()
	os.Chmod(p.config.RecoveryDirectory, 0700)
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	q, _ := New(p.config)
	if err := q.Start(context.Background()); !errors.Is(err, domain.ErrBusy) {
		t.Fatal("second owner", err)
	}
	before := p.Observe()
	if err := p.SetRetention(context.Background(), before.ExecutionID, true); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	store, err := openRecords(p.config.RecoveryDirectory, p.config.ID)
	if err != nil {
		t.Fatal(err)
	}
	original := store.value
	forged := original
	forged.StartTicks++
	if err := store.write(forged); err != nil {
		t.Fatal(err)
	}
	store.close()
	q, _ = New(p.config)
	if err := q.Start(context.Background()); !errors.Is(err, domain.ErrOwnership) {
		t.Fatal("forged identity", err)
	}
	q.Close(context.Background())
	if err := unix.Kill(original.PID, 0); err != nil {
		t.Fatal("unverified process was killed", err)
	}
	store, err = openRecords(p.config.RecoveryDirectory, p.config.ID)
	if err != nil {
		t.Fatal(err)
	}
	store.write(original)
	store.close()
	q, _ = New(p.config)
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := q.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestConsoleCapacityAndRetentionDoNotFollowReplacement(t *testing.T) {
	p := fixtureProcess(t, "output")
	p.config.ID = "console"
	p.config.RecoveryDirectory = t.TempDir()
	os.Chmod(p.config.RecoveryDirectory, 0700)
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(p.config.Configuration + ".ready"); return err == nil })
	first := p.Observe()
	for offset := uint64(0); offset < domain.MaxOutputBytes; {
		page, err := p.ReadOutput(context.Background(), domain.OutputRequest{ExecutionID: first.ExecutionID, Offset: offset})
		if err != nil || !page.CapacityReached || page.NextOffset <= offset || len(page.Data) > domain.MaxOutputRead || len(bytes.Trim(page.Data, "x")) != 0 {
			t.Fatal("console capacity", page.NextOffset, err)
		}
		offset = page.NextOffset
	}
	if p.Observe().State != domain.Running {
		t.Fatal("console overflow terminated child")
	}
	if err := p.SetRetention(context.Background(), first.ExecutionID, true); err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.Observe().PreserveOnClose {
		t.Fatal("retention followed replacement")
	}
	pid := p.current.pid
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := unix.Kill(pid, 0); !errors.Is(err, unix.ESRCH) {
		t.Fatal("replacement retained", err)
	}
}
