package policies_test

import (
	"math"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/policies"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

func TestUsagePreservesSmallDeltasAndDoesNotDoubleCountGuests(t *testing.T) {
	first := domain.Ticks{CPU: "cpu9", User: 1 << 63, Nice: 1 << 63, System: 100, Idle: 1 << 63, IOWait: 100, Guest: 1 << 62, GuestNice: 1 << 62}
	last := first
	last.User += 10
	last.Nice += 10
	last.System += 10
	last.Idle += 50
	last.IOWait += 20
	last.Guest += 5
	last.GuestNice += 5
	got, err := window([]domain.Ticks{first}, []domain.Ticks{last})
	if err != nil || len(got) != 1 || got[0].State != domain.UsageMeasured || got[0].Percent == nil || *got[0].Percent != 30 || got[0].BusyTicks == nil || *got[0].BusyTicks != 30 || got[0].TotalTicks == nil || *got[0].TotalTicks != 100 {
		t.Fatalf("lost integer deltas or double-counted guest time: %+v %v", got, err)
	}
}

func TestUsageDistinguishesIdleFromUnknownAndOverflow(t *testing.T) {
	for _, test := range []struct {
		before, after domain.Ticks
		state         domain.UsageState
		percent       *float64
	}{
		{domain.Ticks{}, domain.Ticks{Idle: 100}, domain.UsageMeasured, new(float64)},
		{domain.Ticks{}, domain.Ticks{}, domain.UsageNoProgress, nil},
		{domain.Ticks{IOWait: 10}, domain.Ticks{IOWait: 9}, domain.UsageCounterDecreased, nil},
		{domain.Ticks{User: 10}, domain.Ticks{User: 9}, domain.UsageCounterDecreased, nil},
		{domain.Ticks{}, domain.Ticks{User: math.MaxUint64, Nice: 1}, domain.UsageCounterOverflow, nil},
	} {
		got, err := window([]domain.Ticks{test.before}, []domain.Ticks{test.after})
		if err != nil || len(got) != 1 || got[0].State != test.state || ((got[0].Percent == nil) != (test.percent == nil)) {
			t.Fatalf("incorrect measurement state: %+v %v; want %s", got, err, test.state)
		}
		if test.percent != nil && *got[0].Percent != *test.percent {
			t.Fatal(*got[0].Percent)
		}
		if test.percent == nil && (got[0].BusyTicks != nil || got[0].TotalTicks != nil) {
			t.Fatal("partial deltas on invalid window")
		}
	}
}

func TestUsageMatchesIdentityAcrossReorderingAndHotplug(t *testing.T) {
	first := []domain.Ticks{{CPU: "cpu2", User: 10}, {CPU: "cpu9", Idle: 10}, {CPU: "cpu7", User: 10}}
	last := []domain.Ticks{{CPU: "cpu9", Idle: 110}, {CPU: "cpu3", User: 50}, {CPU: "cpu2", User: 110}}
	got, err := window(first, last)
	if err != nil || len(got) != 4 {
		t.Fatalf("hotplug window: %+v %v", got, err)
	}
	for i, want := range []struct {
		cpu   string
		state domain.UsageState
	}{{"cpu9", domain.UsageMeasured}, {"cpu3", domain.UsageAdded}, {"cpu2", domain.UsageMeasured}, {"cpu7", domain.UsageRemoved}} {
		if got[i].CPU != want.cpu || got[i].State != want.state {
			t.Fatalf("lost CPU identity/order: %+v", got)
		}
	}
	if *got[0].Percent != 0 || got[1].Percent != nil || *got[2].Percent != 100 || got[3].Percent != nil {
		t.Fatalf("fabricated or cross-CPU percentage: %+v", got)
	}
}

func TestUsageRejectsChangedScopeOrClockUnits(t *testing.T) {
	for _, last := range []domain.Times{{PerCPU: false, TicksPerSecond: 100}, {PerCPU: true, TicksPerSecond: 200}} {
		got, err := policies.Usage(domain.Times{PerCPU: true, TicksPerSecond: 100}, last)
		if err == nil || got != nil {
			t.Fatalf("incompatible counter windows accepted: %+v %v", got, err)
		}
	}
}
