//go:build linux

package process

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

func TestAdmissionBoundsAndOwnerCloseJoinsIndependentWork(t *testing.T) {
	a, err := NewAdmission(2)
	if err != nil {
		t.Fatal(err)
	}
	first, releaseFirst, err := a.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, releaseSecond, err := a.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.acquire(context.Background()); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal("overload was queued/admitted")
	}
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := a.acquire(expired); !errors.Is(err, context.Canceled) {
		t.Fatal("expired input consumed capacity")
	}
	stop, cancelStop := context.WithTimeout(context.Background(), time.Second)
	defer cancelStop()
	done := make(chan error, 1)
	go func() { done <- a.Close(stop) }()
	for _, ctx := range []context.Context{first, second} {
		select {
		case <-ctx.Done():
		case <-stop.Done():
			t.Fatal("owner did not cancel admitted work")
		}
	}
	select {
	case <-done:
		t.Fatal("close returned before admitted work joined")
	default:
	}
	if _, _, err := a.acquire(context.Background()); !errors.Is(err, domain.ErrClosed) {
		t.Fatal("work admitted during close")
	}
	releaseFirst()
	select {
	case <-done:
		t.Fatal("close skipped second active operation")
	default:
	}
	releaseSecond()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := a.Close(expired); err != nil {
		t.Fatal("completed close lost to cancellation")
	}
}

func TestCloseDeadlineDoesNotPretendActiveWorkJoined(t *testing.T) {
	a, _ := NewAdmission(1)
	ctx, release, err := a.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Close(deadline); !errors.Is(err, context.Canceled) {
		t.Fatal("unfinished work reported as closed")
	}
	<-ctx.Done()
	release()
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
