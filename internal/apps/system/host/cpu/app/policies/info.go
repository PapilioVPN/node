package policies

import (
	"math"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

// Info derives package/core counts only from complete, consistent OS reports.
// It does not guess SMT, physical sockets or topology omitted by the kernel.
func Info(rows []domain.Processor) domain.Info {
	result := domain.Info{LogicalCount: uint32(len(rows)), Processors: rows}
	packages := make(map[int32]uint32)
	coresKnown := true
	for _, row := range rows {
		if row.PackageID == nil {
			return result
		}
		previous, exists := packages[*row.PackageID]
		if row.ReportedCores == nil || *row.ReportedCores == 0 {
			coresKnown = false
			packages[*row.PackageID] = previous
			continue
		}
		if exists && previous != *row.ReportedCores {
			coresKnown = false
		}
		packages[*row.PackageID] = *row.ReportedCores
	}
	if len(packages) == 0 {
		return result
	}
	count := uint32(len(packages))
	result.PackageCount = &count
	if !coresKnown {
		return result
	}
	var total uint64
	for _, cores := range packages {
		total += uint64(cores)
	}
	if total > math.MaxUint32 {
		return result
	}
	countCores := uint32(total)
	result.PhysicalCoreCount = &countCores
	return result
}
