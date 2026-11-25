package host

import (
	"testing"

	"github.com/PapilioVPN/node/internal/host/cpu"
	"github.com/PapilioVPN/node/internal/host/memory"
	"github.com/PapilioVPN/node/internal/host/network"
	"github.com/PapilioVPN/node/internal/host/system"
)

func BenchmarkMemory(b *testing.B) {
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := memory.Read(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSystemStatsOSReads(b *testing.B) {
	ctx := b.Context()
	// Measure the live OS reads before a CPU sample is available.
	sampler := cpu.NewSampler()
	netSampler := network.NewSampler()
	b.ReportAllocs()
	for b.Loop() {
		snapshot, err := system.Stats(ctx, sampler, netSampler, "/")
		if err != nil {
			b.Fatal(err)
		}
		if snapshot.UptimeSeconds == nil || snapshot.Memory == nil || snapshot.Storage == nil {
			b.Fatal("unable to benchmark all three OS reads")
		}
	}
}
