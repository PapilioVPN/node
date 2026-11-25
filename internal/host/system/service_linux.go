package system

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/host/cpu"
	"github.com/PapilioVPN/node/internal/host/memory"
	"github.com/PapilioVPN/node/internal/host/network"
	"github.com/PapilioVPN/node/internal/host/storage"
)

type Snapshot struct {
	UptimeSeconds *uint64
	CPU           *cpu.Sample
	LogicalCPUs   uint32
	Network       *network.Sample
	Memory        *memory.Snapshot
	Storage       *storage.Snapshot
}

// Stats returns the available host observations in a single response.
func Stats(ctx context.Context, sampler *cpu.Sampler, netSampler *network.Sampler, storagePath string) (Snapshot, error) {
	snapshot := Snapshot{LogicalCPUs: sampler.LogicalCPUs()}
	seconds, uptimeErr := Uptime(ctx)
	if uptimeErr == nil {
		snapshot.UptimeSeconds = &seconds
	}
	if sample, ok := sampler.Latest(); ok {
		snapshot.CPU = &sample
	}
	if sample, ok := netSampler.Latest(); ok {
		snapshot.Network = &sample
	}
	memorySnapshot, memoryErr := memory.Read(ctx)
	if memoryErr == nil {
		snapshot.Memory = &memorySnapshot
	}
	storageSnapshot, storageErr := storage.Read(ctx, storagePath)
	if storageErr == nil {
		snapshot.Storage = &storageSnapshot
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if snapshot == (Snapshot{}) {
		return snapshot, errors.Join(uptimeErr, memoryErr, storageErr)
	}
	return snapshot, nil
}
