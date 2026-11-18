package policies_test

import (
	"math"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/policies"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

func TestInfoUsesConsistentReportedCoresWithoutCountingSMTTwice(t *testing.T) {
	package0, package1 := int32(0), int32(9)
	four, six := uint32(4), uint32(6)
	rows := []domain.Processor{{ID: 0, PackageID: &package0, ReportedCores: &four}, {ID: 3, PackageID: &package0, ReportedCores: &four}, {ID: 7, PackageID: &package1, ReportedCores: &six}}
	got := policies.Info(rows)
	if got.LogicalCount != 3 || got.PackageCount == nil || *got.PackageCount != 2 || got.PhysicalCoreCount == nil || *got.PhysicalCoreCount != 10 || len(got.Processors) != 3 || got.Processors[1].ID != 3 {
		t.Fatalf("incorrect reported topology: %+v", got)
	}
}

func TestInfoRetainsUnknownAndInconsistentTopology(t *testing.T) {
	pkg := int32(0)
	other := int32(1)
	four, six, max := uint32(4), uint32(6), uint32(math.MaxUint32)
	for _, rows := range [][]domain.Processor{
		{{ID: 0}}, {{ID: 0, PackageID: &pkg}},
		{{ID: 0, PackageID: &pkg, ReportedCores: &four}, {ID: 1, PackageID: &pkg, ReportedCores: &six}},
		{{ID: 0, PackageID: &pkg, ReportedCores: &max}, {ID: 1, PackageID: &other, ReportedCores: &max}},
	} {
		got := policies.Info(rows)
		if got.PhysicalCoreCount != nil {
			t.Fatalf("fabricated complete core count: %+v", got)
		}
	}
	if got := policies.Info([]domain.Processor{{ID: 0}}); got.PackageCount != nil {
		t.Fatal("inferred unknown physical packages")
	}
}
