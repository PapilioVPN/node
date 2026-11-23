package traffic

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

type readerFixture struct {
	read func(context.Context, string, Scope, string) (Usage, error)
}

func (fixture readerFixture) Read(ctx context.Context, id string, scope Scope, name string) (Usage, error) {
	return fixture.read(ctx, id, scope, name)
}

func (fixture readerFixture) List(context.Context, string, Scope) ([]Usage, error) {
	panic("unexpected list")
}

func (readerFixture) Reset(context.Context, Counter) (int64, error) {
	panic("unexpected reset")
}

func (readerFixture) ResetPair(context.Context, CounterPair) (PairReset, error) {
	panic("unexpected pair reset")
}

func TestGetPreservesCounterPresence(t *testing.T) {
	service := New(readerFixture{read: func(ctx context.Context, id string, scope Scope, name string) (Usage, error) {
		if _, timed := ctx.Deadline(); !timed || id != "primary" || scope != User || name != "team>>>alice" {
			t.Fatalf("read selector = %q %q %q", id, scope, name)
		}
		return Usage{Name: name, HasUplink: true}, nil
	}}, false)
	want := Usage{Name: "team>>>alice", HasUplink: true}
	got, err := service.Get(t.Context(), "primary", User, want.Name)
	if err != nil || got != want {
		t.Fatalf("zero uplink and absent downlink: %+v, %v", got, err)
	}
}

func TestGetRejectsInvalidAndCanceledSelectorsBeforeRead(t *testing.T) {
	service := New(readerFixture{}, false)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Get(ctx, "primary", User, "alice"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read = %v", err)
	}
	if _, err := service.Get(t.Context(), "primary", "unsupported", "alice"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid scope = %v", err)
	}
}

func TestReadLimitReleasesSlotOnError(t *testing.T) {
	var active atomic.Int32
	nativeErr := errors.New("native read failed")
	_, err := withLimit(t.Context(), &active, 1, ErrBusy, func(ctx context.Context) (Usage, error) {
		_, err := withLimit(ctx, &active, 1, ErrBusy, func(context.Context) ([]Usage, error) {
			t.Fatal("busy request entered the native reader")
			return nil, nil
		})
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("nested read = %v, want ErrBusy", err)
		}
		return Usage{}, nativeErr
	})
	if !errors.Is(err, nativeErr) {
		t.Fatalf("first read = %v", err)
	}
	got, err := withLimit(t.Context(), &active, 1, ErrBusy, func(context.Context) ([]Usage, error) {
		return []Usage{{Name: "account"}}, nil
	})
	if err != nil || len(got) != 1 || got[0].Name != "account" {
		t.Fatalf("read after release = %+v, %v", got, err)
	}
}

func TestResetRequiresGrantAndExactSelector(t *testing.T) {
	counter := Counter{ID: "primary", Scope: User, Name: "alice", Direction: Uplink}
	if _, err := New(readerFixture{}, false).Reset(t.Context(), counter); !errors.Is(err, ErrResetDisabled) {
		t.Fatalf("disabled reset = %v", err)
	}
	counter.Direction = ""
	if _, err := New(readerFixture{}, true).Reset(t.Context(), counter); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing direction = %v", err)
	}
	counter.Direction = Downlink
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := New(readerFixture{}, true).Reset(ctx, counter); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reset = %v", err)
	}
}
