package proc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/processes/domain"
	"github.com/prometheus/procfs"
)

func TestNativeCountTargetAndCursorHaveFocusedReads(t *testing.T) {
	root := t.TempDir()
	for _, pid := range []int{40, 2, 70, 15} {
		if err := os.Mkdir(filepath.Join(root, strconv.Itoa(pid)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Only PID 40 has a stat file. Count must not inspect those files and Read
	// must not depend on the other processes being readable.
	fields := "S 1 0 0 0 0 0 0 0 0 0 9007199254740993 7 0 0 20 -1 2 0 123456 8192 2"
	for range 20 {
		fields += " 0"
	}
	if err := os.WriteFile(filepath.Join(root, "40", "stat"), []byte("40 (a ) process) "+fields), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := procfs.NewFS(root)
	if err != nil {
		t.Fatal(err)
	}
	reader := &Reader{fs: fs, pageSize: 4096, TicksPerSecond: 100}
	if count, err := reader.Count(context.Background()); err != nil || count != 4 {
		t.Fatalf("count %d %v", count, err)
	}
	process, err := reader.Read(context.Background(), 40)
	if err != nil || process.Name != "a ) process" || process.UserTicks != 9007199254740993 || process.ResidentBytes != 8192 || process.StartTicks != 123456 || process.Nice != -1 {
		t.Fatalf("process %+v %v", process, err)
	}
	first, err := reader.List(context.Background(), domain.Selection{Limit: 2})
	if err != nil || len(first.Entries) != 2 || first.Entries[0].PID != 2 || first.Entries[1].PID != 15 || first.Entries[0].Issue != "not_found" || first.NextAfterPID == nil || *first.NextAfterPID != 15 {
		t.Fatalf("page %+v %v", first, err)
	}
	last, err := reader.List(context.Background(), domain.Selection{AfterPID: 15, Limit: 2})
	if err != nil || len(last.Entries) != 2 || last.Entries[0].Process == nil || last.Entries[1].PID != 70 || last.NextAfterPID != nil {
		t.Fatalf("last %+v %v", last, err)
	}
	if _, err := reader.Read(context.Background(), 99); !errors.Is(err, domain.ErrNotFound) || !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.Count(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := reader.Read(ctx, 40); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := reader.List(ctx, domain.Selection{Limit: 2}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPageCallGrowthConcurrencyAndPartialFailures(t *testing.T) {
	for _, size := range []int{0, 1, 64, 256} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			rows := make(procfs.Procs, size)
			for i := range rows {
				rows[i] = procfs.Proc{PID: i + 1}
			}
			var active, peak, calls atomic.Int32
			page, err := readPage(context.Background(), rows, 4096, func(row procfs.Proc) (procfs.ProcStat, error) {
				calls.Add(1)
				current := active.Add(1)
				defer active.Add(-1)
				for old := peak.Load(); current > old; old = peak.Load() {
					if peak.CompareAndSwap(old, current) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				switch row.PID {
				case 2:
					return procfs.ProcStat{}, os.ErrNotExist
				case 3:
					return procfs.ProcStat{}, os.ErrPermission
				case 4:
					return procfs.ProcStat{}, errors.New("private native cause")
				}
				return procfs.ProcStat{PID: row.PID, State: "S"}, nil
			})
			if err != nil || int(calls.Load()) != size || active.Load() != 0 || peak.Load() > 8 || len(page.Entries) != size {
				t.Fatalf("size=%d calls=%d active=%d peak=%d err=%v", size, calls.Load(), active.Load(), peak.Load(), err)
			}
			if size > 4 && (page.Entries[1].Issue != "not_found" || page.Entries[2].Issue != "permission_denied" || page.Entries[3].Issue != "unavailable" || page.Entries[4].Process.PID != 5) {
				t.Fatalf("partial results lost: %+v", page.Entries)
			}
		})
	}
}

func TestCanceledPageJoinsStartedReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var active, calls atomic.Int32
	rows := make(procfs.Procs, 256)
	for i := range rows {
		rows[i].PID = i + 1
	}
	_, err := readPage(ctx, rows, 4096, func(row procfs.Proc) (procfs.ProcStat, error) {
		active.Add(1)
		defer active.Add(-1)
		if calls.Add(1) == 1 {
			cancel()
		}
		time.Sleep(time.Millisecond)
		return procfs.ProcStat{PID: row.PID, State: "S"}, nil
	})
	if !errors.Is(err, context.Canceled) || active.Load() != 0 || calls.Load() > 8 {
		t.Fatalf("unjoined cancellation calls=%d active=%d err=%v", calls.Load(), active.Load(), err)
	}
}

func TestConversionRejectsOverflowAndInvalidText(t *testing.T) {
	for _, stat := range []procfs.ProcStat{{PID: 1, State: "S", RSS: -1}, {PID: 1, State: "S", RSS: math.MaxInt64}, {PID: 1, State: "S", Comm: string([]byte{255})}, {PID: 1, State: ""}} {
		if _, err := convert(stat, 4096); err == nil {
			t.Fatal(fmt.Sprintf("accepted invalid native stat: %+v", stat))
		}
	}
	if err := classify(os.ErrPermission); !errors.Is(err, domain.ErrPermissionDenied) || !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
}
