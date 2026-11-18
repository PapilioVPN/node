package sys

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/domain"
	"github.com/prometheus/procfs"
	"golang.org/x/sys/unix"
)

func TestMountPagesReadMetadataWithoutCapacityProbes(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "self"), 0700); err != nil {
		t.Fatal(err)
	}
	contents := "42 1 8:1 /root\\040dir /not\\040present ro - ext4 /dev/a rw\n2 1 0:2 / /quote\"name rw - tmpfs tmpfs rw\n"
	if err := os.WriteFile(filepath.Join(root, "self", "mountinfo"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := procfs.NewFS(root)
	if err != nil {
		t.Fatal(err)
	}
	reader := &Reader{fs: fs, statfs: func(string, *unix.Statfs_t) error { t.Fatal("inventory probed capacity"); return nil }}
	page, err := reader.List(context.Background(), domain.Selection{Limit: 1})
	if err != nil || len(page.Mounts) != 1 || page.Mounts[0].ID != 2 || page.Mounts[0].Path != "/quote\"name" || page.NextAfterID == nil || *page.NextAfterID != 2 {
		t.Fatalf("%+v %v", page, err)
	}
	page, err = reader.List(context.Background(), domain.Selection{AfterID: 2, Limit: 1})
	if err != nil || len(page.Mounts) != 1 || page.Mounts[0].Path != "/not present" || page.Mounts[0].Root != "/root dir" || !page.Mounts[0].ReadOnly || page.Mounts[0].DeviceMajor != 8 || page.Mounts[0].DeviceMinor != 1 || page.NextAfterID != nil {
		t.Fatalf("%+v %v", page, err)
	}
}
func TestCapacityPreservesReservedBlocksExactUnitsAndNativeErrors(t *testing.T) {
	calls := 0
	reader := &Reader{statfs: func(path string, stat *unix.Statfs_t) error {
		calls++
		if path != "/selected" {
			t.Fatal(path)
		}
		*stat = unix.Statfs_t{Type: 0xef53, Bsize: 4096, Blocks: 9007199254740993/4096 + 1, Bfree: 10, Bavail: 7, Files: 100, Ffree: 60}
		return nil
	}}
	usage, err := reader.Usage(context.Background(), "/selected")
	if err != nil || calls != 1 || usage.TotalBytes <= 9007199254740992 || usage.ReservedBytes != 3*4096 || usage.InodesUsed != 40 || usage.FreeBytes != 10*4096 || usage.AvailableBytes != 7*4096 {
		t.Fatalf("%+v %v", usage, err)
	}
	for _, failure := range []struct {
		cause    error
		expected error
	}{{unix.ENOENT, domain.ErrNotFound}, {unix.ENOTDIR, domain.ErrNotFound}, {unix.EACCES, domain.ErrPermissionDenied}, {unix.EPERM, domain.ErrPermissionDenied}, {unix.EIO, unix.EIO}} {
		reader.statfs = func(string, *unix.Statfs_t) error { return failure.cause }
		if _, err := reader.Usage(context.Background(), "/selected"); !errors.Is(err, failure.expected) || !errors.Is(err, failure.cause) {
			t.Fatal(err)
		}
	}
	for _, stat := range []unix.Statfs_t{{Bsize: 0}, {Bsize: 4096, Blocks: math.MaxUint64}, {Bsize: 1, Blocks: 10, Bfree: 11}, {Bsize: 1, Blocks: 10, Bfree: 5, Bavail: 6}, {Bsize: 1, Files: 1, Ffree: 2}} {
		reader.statfs = func(_ string, target *unix.Statfs_t) error { *target = stat; return nil }
		if _, err := reader.Usage(context.Background(), "/selected"); err == nil {
			t.Fatal("accepted invalid capacity")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.Usage(ctx, "/selected"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := reader.List(ctx, domain.Selection{Limit: 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
