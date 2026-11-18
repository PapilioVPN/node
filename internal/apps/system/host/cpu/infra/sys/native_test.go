//go:build integration && linux

package sys_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/infra/sys"
)

func TestActualHostLoadMatchesProcAverages(t *testing.T) {
	before := procLoad(t)
	got, err := (sys.Reader{}).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	after := procLoad(t)
	for i, value := range []float64{got.OneMinute, got.FiveMinutes, got.FifteenMinutes} {
		// /proc/loadavg rounds the same kernel averages to two decimal places.
		if value < math.Min(before[i], after[i])-0.01 || value > math.Max(before[i], after[i])+0.01 {
			t.Fatalf("load window %d: %v outside proc observations %v / %v", i, got, before, after)
		}
	}
}

func TestCanceledOrExpiredReadReturnsNoLoad(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, ctx := range []context.Context{canceled, expired} {
		got, err := (sys.Reader{}).Read(ctx)
		if got != (domain.Averages{}) || !errors.Is(err, ctx.Err()) {
			t.Fatalf("unexpected canceled read: %+v %v", got, err)
		}
	}
}

func procLoad(t *testing.T) [3]float64 {
	t.Helper()
	contents, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		t.Fatal(err)
	}
	var result [3]float64
	if _, err := fmt.Sscanf(string(contents), "%f %f %f", &result[0], &result[1], &result[2]); err != nil {
		t.Fatal(err)
	}
	return result
}
