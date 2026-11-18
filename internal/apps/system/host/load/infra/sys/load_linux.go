// Package sys reads Linux system-wide load through the kernel interface.
package sys

import (
	"context"
	"fmt"

	"github.com/PapilioVPN/node/internal/apps/system/host/load/domain"
	"golang.org/x/sys/unix"
)

type Reader struct{}

// Read uses one sysinfo syscall; no proc mount, sampling wait or worker is needed.
// Cancellation is checked around the synchronous syscall, not interrupting it.
func (Reader) Read(ctx context.Context) (domain.Averages, error) {
	if err := ctx.Err(); err != nil {
		return domain.Averages{}, err
	}
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return domain.Averages{}, fmt.Errorf("read host load: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return domain.Averages{}, err
	}
	return averages(uint64(info.Loads[0]), uint64(info.Loads[1]), uint64(info.Loads[2])), nil
}

func averages(one, five, fifteen uint64) domain.Averages {
	// Linux's sysinfo ABI defines SI_LOAD_SHIFT=16; load averages are counts.
	const scale = 1 << 16
	return domain.Averages{OneMinute: float64(one) / scale, FiveMinutes: float64(five) / scale, FifteenMinutes: float64(fifteen) / scale}
}
