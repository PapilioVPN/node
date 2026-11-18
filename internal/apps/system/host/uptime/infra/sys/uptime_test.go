//go:build integration && linux

package sys_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/uptime/infra/sys"
)

func TestUptimeMatchesActualLinuxBootClock(t *testing.T) {
	// /proc/uptime truncates to centiseconds; bracket the native read instead of
	// relying on a wall-clock subtraction or scheduling tolerance.
	before := procUptime(t)
	got, err := (sys.Reader{}).Read(context.Background())
	after := procUptime(t)
	if err != nil || got < before || got >= after+10*time.Millisecond {
		t.Fatalf("native uptime %v, error %v; proc bracket [%v, %v)", got, err, before, after+10*time.Millisecond)
	}
}

func TestCanceledAndExpiredReadsReturnNoUptime(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	for _, ctx := range []context.Context{canceled, expired} {
		got, err := (sys.Reader{}).Read(ctx)
		if !errors.Is(err, ctx.Err()) || got != 0 {
			t.Fatalf("read with completed context: %v, %v", got, err)
		}
	}
}
