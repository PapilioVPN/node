package sys

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/shirou/gopsutil/v4/common"
	nativecpu "github.com/shirou/gopsutil/v4/cpu"
	"github.com/tklauser/go-sysconf"
)

type TimesReader struct{}

// Read uses the SDK's targeted aggregate or bulk per-CPU path, one stat file per
// observation. Context checks surround synchronous I/O; they do not interrupt it.
func (TimesReader) Read(ctx context.Context, perCPU bool) (domain.Times, error) {
	return readTimes(ctx, "/proc", perCPU)
}

func readTimes(ctx context.Context, procRoot string, perCPU bool) (domain.Times, error) {
	if err := ctx.Err(); err != nil {
		return domain.Times{}, err
	}
	ctx = context.WithValue(ctx, common.EnvKey, common.EnvMap{common.HostProcEnvKey: procRoot})
	native, err := nativecpu.NewExLinux().TimesWithContext(ctx, perCPU)
	if err != nil {
		return domain.Times{}, errors.Join(ctx.Err(), fmt.Errorf("read host CPU ticks: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return domain.Times{}, err
	}
	if len(native) == 0 || (!perCPU && len(native) != 1) {
		return domain.Times{}, errors.New("missing selected CPU counters")
	}
	rate, err := sysconf.Sysconf(sysconf.SC_CLK_TCK)
	if err != nil {
		return domain.Times{}, fmt.Errorf("read CPU tick rate: %w", err)
	}
	if rate <= 0 {
		return domain.Times{}, errors.New("invalid CPU tick rate")
	}
	result := domain.Times{PerCPU: perCPU, TicksPerSecond: uint64(rate), Counters: make([]domain.Ticks, 0, len(native))}
	seen := make(map[string]struct{}, len(native))
	for _, row := range native {
		if err := ctx.Err(); err != nil {
			return domain.Times{}, err
		}
		if perCPU {
			suffix, ok := strings.CutPrefix(row.CPU, "cpu")
			id, err := strconv.ParseUint(suffix, 10, 32)
			if !ok || err != nil || strconv.FormatUint(id, 10) != suffix {
				return domain.Times{}, errors.New("invalid CPU counter identity")
			}
		} else if row.CPU != "cpu-total" {
			return domain.Times{}, errors.New("missing aggregate CPU counters")
		}
		if _, exists := seen[row.CPU]; exists {
			return domain.Times{}, errors.New("duplicate CPU counter identity")
		}
		seen[row.CPU] = struct{}{}
		result.Counters = append(result.Counters, domain.Ticks{CPU: row.CPU, User: row.User, Nice: row.Nice, System: row.System, Idle: row.Idle, IOWait: row.Iowait, IRQ: row.Irq, SoftIRQ: row.Softirq, Steal: row.Steal, Guest: row.Guest, GuestNice: row.GuestNice})
	}
	return result, nil
}
