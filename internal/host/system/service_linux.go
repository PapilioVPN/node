package system

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/host/cpu"
	"github.com/PapilioVPN/node/internal/host/memory"
	"github.com/PapilioVPN/node/internal/host/storage"
)

type Snapshot struct {
	UptimeSeconds *uint64
	CPU           *cpu.Sample
	Memory        *memory.Snapshot
	Storage       *storage.Snapshot
}

// Stats returns the available host observations in a single response.
func Stats(ctx context.Context, sampler *cpu.Sampler, storagePath string) (Snapshot, error) {
	var snapshot Snapshot
	seconds, uptimeErr := Uptime(ctx)
	if uptimeErr == nil {
		snapshot.UptimeSeconds = &seconds
	}
	if sample, ok := sampler.Latest(); ok {
		snapshot.CPU = &sample
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
