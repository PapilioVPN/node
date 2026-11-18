package proc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/domain"
	"github.com/prometheus/procfs/blockdevice"
)

func TestBulkCallGrowthAndPageSelection(t *testing.T) {
	for _, size := range []int{0, 1, 64, 256, 4096} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			rows := make([]blockdevice.Diskstats, size)
			for i := range rows {
				rows[i] = blockdevice.Diskstats{Info: blockdevice.Info{MajorNumber: 8, MinorNumber: uint32(i), DeviceName: fmt.Sprintf("disk%05d", size-i)}, IoStatsCount: 14}
			}
			calls := 0
			reader := &Reader{read: func() ([]blockdevice.Diskstats, error) { calls++; return rows, nil }}
			page, err := reader.List(context.Background(), domain.Selection{Limit: 128})
			if err != nil || calls != 1 || len(page.Devices) != min(size, 128) || (size > 128) != (page.NextAfterName != nil) {
				t.Fatalf("size=%d calls=%d page=%+v err=%v", size, calls, page, err)
			}
			if size > 0 && page.Devices[0].Name != "disk00001" {
				t.Fatal("unsorted native page")
			}
			if size > 128 {
				next, err := reader.List(context.Background(), domain.Selection{AfterName: *page.NextAfterName, Limit: 1})
				if err != nil || len(next.Devices) != 1 || next.Devices[0].Name != "disk00129" {
					t.Fatalf("bad cursor: %+v %v", next, err)
				}
			}
		})
	}
}
func TestNativeLayoutsPreservePrecisionAndOptionalCounters(t *testing.T) {
	root := t.TempDir()
	data := "8 0 sda 1 2 9007199254740993 4 5 6 7 8 9 10 11\n8 1 sda1 1 2 3 4 5 6 7 8 9 10 11 0 0 0 0\n8 2 sda2 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17\n"
	if err := os.WriteFile(filepath.Join(root, "diskstats"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := blockdevice.NewFS(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	page, err := (&Reader{read: fs.ProcDiskstats}).List(context.Background(), domain.Selection{Limit: 3})
	if err != nil || len(page.Devices) != 3 || page.Devices[0].ReadSectors != 9007199254740993 || page.Devices[0].Discard != nil || page.Devices[0].Flush != nil || page.Devices[1].Discard == nil || page.Devices[1].Discard.Completed != 0 || page.Devices[1].Flush != nil || page.Devices[2].Flush == nil || page.Devices[2].Flush.Milliseconds != 17 {
		t.Fatalf("%+v %v", page, err)
	}
}
func TestInvalidRecordsFailuresAndCancellation(t *testing.T) {
	cause := errors.New("native cause")
	reader := &Reader{read: func() ([]blockdevice.Diskstats, error) { return nil, cause }}
	if _, err := reader.List(context.Background(), domain.Selection{Limit: 1}); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader.read = func() ([]blockdevice.Diskstats, error) { t.Fatal("canceled read reached native I/O"); return nil, nil }
	if _, err := reader.List(ctx, domain.Selection{Limit: 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, count := range []int{0, 13, 15, 17, 19} {
		if _, err := device(blockdevice.Diskstats{Info: blockdevice.Info{DeviceName: "sda"}, IoStatsCount: count}); err == nil {
			t.Fatal("invented missing counters")
		}
	}
	row := blockdevice.Diskstats{Info: blockdevice.Info{DeviceName: "sda"}, IoStatsCount: 14}
	reader.read = func() ([]blockdevice.Diskstats, error) { return []blockdevice.Diskstats{row, row}, nil }
	if _, err := reader.List(context.Background(), domain.Selection{Limit: 1}); err == nil {
		t.Fatal("accepted duplicate identity")
	}
}
