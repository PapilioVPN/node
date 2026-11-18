// Package sys reads Linux uptime through the native boot-time clock.
package sys

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"golang.org/x/sys/unix"
)

type Reader struct{}

// Read performs one CLOCK_BOOTTIME read in the current time namespace.
// Cancellation is checked around the synchronous syscall; it cannot interrupt it.
func (Reader) Read(ctx context.Context) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var elapsed unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &elapsed); err != nil {
		return 0, fmt.Errorf("read host uptime: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return duration(elapsed)
}

func duration(elapsed unix.Timespec) (time.Duration, error) {
	const second = int64(time.Second)
	if elapsed.Sec < 0 || elapsed.Nsec < 0 || elapsed.Nsec >= second || elapsed.Sec > (math.MaxInt64-elapsed.Nsec)/second {
		return 0, errors.New("host uptime is outside the supported duration range")
	}
	return time.Duration(elapsed.Sec*second + elapsed.Nsec), nil
}
