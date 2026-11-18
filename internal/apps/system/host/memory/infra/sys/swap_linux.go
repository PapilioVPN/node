package sys

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
	"golang.org/x/sys/unix"
)

type SwapReader struct{}

// Read performs one sysinfo call and one bounded vmstat read. Context checks
// surround synchronous kernel/filesystem I/O; they cannot interrupt that I/O.
func (SwapReader) Read(ctx context.Context) (domain.SwapSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return domain.SwapSnapshot{}, err
	}
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return domain.SwapSnapshot{}, fmt.Errorf("read swap capacity: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return domain.SwapSnapshot{}, err
	}
	file, err := os.Open("/proc/vmstat")
	if err != nil {
		return domain.SwapSnapshot{}, fmt.Errorf("open swap counters: %w", err)
	}
	in, out, readErr := readIO(ctx, file, uint64(os.Getpagesize()))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return domain.SwapSnapshot{}, fmt.Errorf("read swap counters: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return domain.SwapSnapshot{}, err
	}
	return capacity(uint64(info.Totalswap), uint64(info.Freeswap), uint64(info.Unit), in, out)
}
func capacity(total, free, unit, in, out uint64) (domain.SwapSnapshot, error) {
	if free > total {
		return domain.SwapSnapshot{}, errors.New("swap free exceeds total capacity")
	}
	totalBytes, err := scaledBytes(total, unit)
	if err != nil {
		return domain.SwapSnapshot{}, err
	}
	freeBytes, err := scaledBytes(free, unit)
	if err != nil {
		return domain.SwapSnapshot{}, err
	}
	used := totalBytes - freeBytes
	percent := 0.0
	if totalBytes != 0 {
		percent = float64(used) / float64(totalBytes) * 100
	}
	return domain.SwapSnapshot{TotalBytes: totalBytes, UsedBytes: used, FreeBytes: freeBytes, SwappedInBytes: in, SwappedOutBytes: out, UsedPercent: percent}, nil
}
func scaledBytes(value, unit uint64) (uint64, error) {
	if unit == 0 || value > math.MaxUint64/unit {
		return 0, errors.New("invalid or overflowing swap byte conversion")
	}
	return value * unit, nil
}
