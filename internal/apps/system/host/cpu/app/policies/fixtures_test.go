package policies_test

import (
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/policies"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

func window(before, after []domain.Ticks) ([]domain.Usage, error) {
	return policies.Usage(domain.Times{PerCPU: true, TicksPerSecond: 100, Counters: before}, domain.Times{PerCPU: true, TicksPerSecond: 100, Counters: after})
}
