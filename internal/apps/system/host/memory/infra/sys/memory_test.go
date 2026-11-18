package sys

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
	"github.com/shirou/gopsutil/v4/common"
)

func TestNativeSDKReadPreservesBytesAndLinuxMemorySemantics(t *testing.T) {
	t.Setenv("HOST_PROC", "/unused-environment-source")
	ctx := context.WithValue(context.Background(), common.EnvKey, common.EnvMap{common.HostProcEnvKey: "/unused-caller-source"})
	got, err := read(ctx, procFixture(t, meminfo))
	want := domain.Snapshot{TotalBytes: 1048576, AvailableBytes: 262144, UsedBytes: 786432, FreeBytes: 131072, ActiveBytes: 65536, InactiveBytes: 32768, BuffersBytes: 16384, CachedBytes: 163840, SharedBytes: 8192, SlabBytes: 49152, UsedPercent: 75}
	if err != nil || got != want {
		t.Fatalf("memory conversion: %+v %v; want %+v", got, err, want)
	}
	// Only meminfo exists: modern MemAvailable must not trigger legacy zone reads.
}

func TestNativeFailuresReturnNoMemoryAndPreserveTheirCause(t *testing.T) {
	_, missing := read(context.Background(), t.TempDir())
	var pathError *os.PathError
	if !errors.As(missing, &pathError) || !errors.Is(missing, os.ErrNotExist) {
		t.Fatalf("lost filesystem cause: %v", missing)
	}
	_, malformed := read(context.Background(), procFixture(t, strings.Replace(meminfo, "1024 kB", "invalid kB", 1)))
	var numberError *strconv.NumError
	if !errors.As(malformed, &numberError) {
		t.Fatalf("lost native parse cause: %v", malformed)
	}
	for _, input := range []string{"", strings.Replace(meminfo, "MemAvailable: 256", "MemAvailable: 2048", 1), strings.Replace(meminfo, "MemFree: 128", "MemFree: 2048", 1)} {
		got, err := read(context.Background(), procFixture(t, input))
		if err == nil || got != (domain.Snapshot{}) {
			t.Fatalf("invalid native observation accepted: %+v %v", got, err)
		}
	}
}

func TestCanceledReadAvoidsNativeIO(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := read(ctx, "/missing-source")
	if got != (domain.Snapshot{}) || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %+v %v", got, err)
	}
}
