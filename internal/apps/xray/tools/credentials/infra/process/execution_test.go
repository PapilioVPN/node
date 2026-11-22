package process

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

func TestAdmissionGlobalInstanceCancelAndClose(t *testing.T) {
	a, owner := testRunner(t)
	b, _ := New(a.config, owner)
	var releases []context.CancelFunc
	for _, r := range []*Runner{a, a, b, b} {
		ctx, release, err := r.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > domain.ExecutionTimeout {
			t.Fatal("missing execution deadline")
		}
		releases = append(releases, release)
	}
	if _, _, err := a.acquire(context.Background()); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal("unbounded admission")
	}
	releases[3]()
	releases = releases[:3]
	if _, _, err := a.acquire(context.Background()); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal("per-instance admission bypassed")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := b.acquire(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- owner.Close(context.Background()) }()
	select {
	case <-owner.lifetime.Done():
	case <-time.After(time.Second):
		t.Fatal("close did not cancel lifetime")
	}
	for _, release := range releases {
		release()
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not join")
	}
	if _, _, err := b.acquire(context.Background()); !errors.Is(err, domain.ErrClosed) {
		t.Fatal("closed owner admitted work")
	}
}
func TestOutputBoundsCancelAndNeverRetainExcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := output{cancel: cancel}
	if _, err := o.Write(repeated(domain.MaxOutputBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Write([]byte("secret-excess")); !errors.Is(err, domain.ErrCapacity) || !o.overflow || ctx.Err() == nil || o.Len() != domain.MaxOutputBytes {
		t.Fatal("output bound failed")
	}
}
