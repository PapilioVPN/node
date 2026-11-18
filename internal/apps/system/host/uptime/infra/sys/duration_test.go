//go:build linux

package sys

import (
	"math"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDurationPreservesExactNanoseconds(t *testing.T) {
	for _, value := range []time.Duration{0, 1, time.Second - 1, 37*time.Hour + 123456789, time.Duration(math.MaxInt64)} {
		got, err := duration(unix.Timespec{Sec: int64(value / time.Second), Nsec: int64(value % time.Second)})
		if err != nil || got != value {
			t.Fatalf("conversion of %v: %v, %v", value, got, err)
		}
	}
}

func TestDurationRejectsNegativeUnnormalizedAndOverflowingValues(t *testing.T) {
	for _, elapsed := range []unix.Timespec{
		{Sec: -1}, {Nsec: -1}, {Nsec: int64(time.Second)},
		{Sec: math.MaxInt64 / int64(time.Second), Nsec: math.MaxInt64%int64(time.Second) + 1},
		{Sec: math.MaxInt64},
	} {
		if got, err := duration(elapsed); err == nil || got != 0 {
			t.Fatalf("accepted invalid native clock value %+v: %v, %v", elapsed, got, err)
		}
	}
}
