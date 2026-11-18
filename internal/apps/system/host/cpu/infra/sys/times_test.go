package sys

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/shirou/gopsutil/v4/common"
)

func TestNativeTicksPreservePrecisionIdentityAndSelectedOrder(t *testing.T) {
	root := procStat(t, "cpu 18446744073709551615 2 3 4 5 6 7 8 9 10\ncpu9 11 12 13 14 15 16 17 18 19 20\ncpu2 21 22 23 24 25 26 27 28 29 30\nintr 123\n")
	aggregate, err := readTimes(context.Background(), root, false)
	if err != nil || aggregate.PerCPU || aggregate.TicksPerSecond == 0 || !reflect.DeepEqual(aggregate.Counters, []domain.Ticks{{CPU: "cpu-total", User: 1<<64 - 1, Nice: 2, System: 3, Idle: 4, IOWait: 5, IRQ: 6, SoftIRQ: 7, Steal: 8, Guest: 9, GuestNice: 10}}) {
		t.Fatalf("aggregate: %+v %v", aggregate, err)
	}
	cores, err := readTimes(context.Background(), root, true)
	want := []domain.Ticks{{CPU: "cpu9", User: 11, Nice: 12, System: 13, Idle: 14, IOWait: 15, IRQ: 16, SoftIRQ: 17, Steal: 18, Guest: 19, GuestNice: 20}, {CPU: "cpu2", User: 21, Nice: 22, System: 23, Idle: 24, IOWait: 25, IRQ: 26, SoftIRQ: 27, Steal: 28, Guest: 29, GuestNice: 30}}
	if err != nil || !cores.PerCPU || cores.TicksPerSecond != aggregate.TicksPerSecond || !reflect.DeepEqual(cores.Counters, want) {
		t.Fatalf("per-CPU: %+v %v", cores, err)
	}
}

func TestNativeSelectionDoesNotParseUnrequestedRowsOrUseCallerSource(t *testing.T) {
	root := procStat(t, "cpu 0 0 0 0 0 0 0\ncpu0 malformed\n")
	t.Setenv("HOST_PROC", t.TempDir())
	ctx := context.WithValue(context.Background(), common.EnvKey, common.EnvMap{common.HostProcEnvKey: t.TempDir()})
	got, err := readTimes(ctx, root, false)
	if err != nil || len(got.Counters) != 1 || got.Counters[0] != (domain.Ticks{CPU: "cpu-total"}) {
		t.Fatalf("targeted zero ticks: %+v %v", got, err)
	}
	if _, err := readTimes(ctx, root, true); err == nil {
		t.Fatal("malformed selected CPU row accepted")
	}
}

func TestNativeErrorsAndCancellationReturnNoPartialTimes(t *testing.T) {
	for _, test := range []struct {
		source string
		perCPU bool
	}{
		{"", false}, {"cpu 0 0 0 0 0 0 0\n", true}, {"cpu0 0 0 0 0 0 0 0\n", false},
		{"cpu 0 0 0 0 0 0 0\ncpuX 0 0 0 0 0 0 0\n", true},
		{"cpu 0 0 0 0 0 0 0\ncpu0 0 0 0 0 0 0 0\ncpu0 0 0 0 0 0 0 0\n", true},
		{"cpu 0 0 0 0 0 0 0\ncpu0 0 0 0 0 0 0 0\ncpu2 bad 0 0 0 0 0 0\n", true},
	} {
		got, err := readTimes(context.Background(), procStat(t, test.source), test.perCPU)
		if err == nil || !reflect.DeepEqual(got, domain.Times{}) {
			t.Fatalf("partial/invalid ticks: %+v %v", got, err)
		}
	}
	_, err := readTimes(context.Background(), t.TempDir(), false)
	var path *os.PathError
	if !errors.As(err, &path) {
		t.Fatalf("lost file cause: %v", err)
	}
	_, err = readTimes(context.Background(), procStat(t, "cpu invalid 0 0 0 0 0 0\n"), false)
	var parse *strconv.NumError
	if !errors.As(err, &parse) {
		t.Fatalf("lost parse cause: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := readTimes(ctx, t.TempDir(), true); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, domain.Times{}) {
		t.Fatalf("canceled read: %+v %v", got, err)
	}
}
