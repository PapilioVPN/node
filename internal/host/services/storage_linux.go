package services

import (
	"context"
	"errors"
	"fmt"
	"math"

	"golang.org/x/sys/unix"
)

type StorageSnapshot struct {
	Path           string
	TotalBytes     uint64
	UsedBytes      uint64
	AvailableBytes uint64
	ReadOnly       bool
}

func Storage(ctx context.Context, path string) (StorageSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return StorageSnapshot{}, err
	}
	var stats unix.Statfs_t
	statErr := unix.Statfs(path, &stats)
	if err := ctx.Err(); err != nil {
		return StorageSnapshot{}, err
	}
	if statErr != nil {
		return StorageSnapshot{}, fmt.Errorf("stat Linux filesystem: %w", statErr)
	}
	return storageSnapshot(path, stats)
}

func storageSnapshot(path string, stats unix.Statfs_t) (StorageSnapshot, error) {
	blockSize := stats.Frsize
	if blockSize == 0 {
		blockSize = stats.Bsize
	}
	if blockSize <= 0 || stats.Bfree > stats.Blocks || stats.Bavail > stats.Blocks {
		return StorageSnapshot{}, errors.New("invalid Linux filesystem capacity counters")
	}
	bytesPerBlock := uint64(blockSize)
	if stats.Blocks > math.MaxUint64/bytesPerBlock {
		return StorageSnapshot{}, errors.New("Linux filesystem capacity overflows a byte count")
	}
	return StorageSnapshot{
		Path:           path,
		TotalBytes:     stats.Blocks * bytesPerBlock,
		UsedBytes:      (stats.Blocks - stats.Bfree) * bytesPerBlock,
		AvailableBytes: stats.Bavail * bytesPerBlock,
		ReadOnly:       stats.Flags&unix.ST_RDONLY != 0,
	}, nil
}
