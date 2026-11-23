package host

import (
	"testing"

	"github.com/PapilioVPN/node/internal/host/services"
)

func BenchmarkMemory(b *testing.B) {
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := services.Memory(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSystemStatsOSReads(b *testing.B) {
	ctx := b.Context()
	// Measure the live OS reads before a CPU sample is available.
	sampler := services.NewCPUSampler()
	b.ReportAllocs()
	for b.Loop() {
		snapshot, err := services.SystemStats(ctx, sampler, "/")
		if err != nil {
			b.Fatal(err)
		}
		if snapshot.UptimeSeconds == nil || snapshot.Memory == nil || snapshot.Storage == nil {
			b.Fatal("unable to benchmark all three OS reads")
		}
	}
}
