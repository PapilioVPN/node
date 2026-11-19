//go:build linux

package sys

import (
	"math"
	"testing"

	"github.com/prometheus/procfs"
)

func TestProcessResourceUnitsPrecisionAndOverflow(t *testing.T) {
	stat := procfs.ProcStat{PID: 42, RSS: 3, NumThreads: 2, Starttime: 9007199254740993, UTime: 17, STime: 23, VSize: 9007199254740993}
	result, err := processResources(stat, 100, 4096)
	if err != nil || result.ResidentBytes != 12288 || result.StartTicks != 9007199254740993 || result.VirtualBytes != 9007199254740993 || result.UserTicks != 17 || result.SystemTicks != 23 {
		t.Fatalf("%+v %v", result, err)
	}
	stat.RSS = math.MaxInt
	if _, err := processResources(stat, 100, 4096); err == nil {
		t.Fatal("overflow accepted")
	}
	stat.RSS = 1
	if _, err := processResources(stat, 0, 4096); err == nil {
		t.Fatal("invalid clock scale")
	}
}
