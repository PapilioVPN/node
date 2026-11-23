package host

import (
	"context"
	"fmt"

	"golang.org/x/sys/unix"
)

func readUptime(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var elapsed unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &elapsed); err != nil {
		return 0, fmt.Errorf("read Linux boot clock: %w", err)
	}
	return uint64(elapsed.Sec), nil
}
