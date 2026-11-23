package storage

import (
	"math"
	"testing"

	"golang.org/x/sys/unix"
)

func TestStorageSnapshotPreservesReservedSpaceAndReadOnly(t *testing.T) {
	stats := unix.Statfs_t{
		Bsize: 4096, Frsize: 512, Blocks: 100, Bfree: 30, Bavail: 25, Flags: unix.ST_RDONLY,
	}
	want := Snapshot{Path: "/data", TotalBytes: 51200, UsedBytes: 35840, AvailableBytes: 12800, ReadOnly: true}
	got, err := storageSnapshot("/data", stats)
	if err != nil || got != want {
		t.Fatalf("storage = %+v, %v, want %+v", got, err, want)
	}

	stats.Frsize = 0
	stats.Bavail = 0
	stats.Flags = 0
	want = Snapshot{Path: "/data", TotalBytes: 409600, UsedBytes: 286720}
	got, err = storageSnapshot("/data", stats)
	if err != nil || got != want {
		t.Fatalf("storage = %+v, %v, want %+v", got, err, want)
	}
}

func TestStorageSnapshotRejectsInvalidCounters(t *testing.T) {
	for _, stats := range []unix.Statfs_t{
		{Blocks: 1},
		{Frsize: 512, Blocks: 1, Bfree: 2},
		{Frsize: 512, Blocks: 1, Bavail: 2},
		{Frsize: 512, Blocks: math.MaxUint64},
	} {
		if _, err := storageSnapshot("/data", stats); err == nil {
			t.Errorf("accepted invalid counters %+v", stats)
		}
	}
}
