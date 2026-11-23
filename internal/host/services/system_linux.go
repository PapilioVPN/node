package services

import (
	"context"
	"errors"
)

type SystemSnapshot struct {
	UptimeSeconds *uint64
	CPU           *CPUSample
	Memory        *MemorySnapshot
	Storage       *StorageSnapshot
}

// SystemStats returns the available host observations in a single response.
func SystemStats(ctx context.Context, sampler *CPUSampler, storagePath string) (SystemSnapshot, error) {
	var snapshot SystemSnapshot
	seconds, uptimeErr := Uptime(ctx)
	if uptimeErr == nil {
		snapshot.UptimeSeconds = &seconds
	}
	if sample, ok := sampler.Latest(); ok {
		snapshot.CPU = &sample
	}
	memory, memoryErr := Memory(ctx)
	if memoryErr == nil {
		snapshot.Memory = &memory
	}
	storage, storageErr := Storage(ctx, storagePath)
	if storageErr == nil {
		snapshot.Storage = &storage
	}
	if err := ctx.Err(); err != nil {
		return SystemSnapshot{}, err
	}
	if snapshot == (SystemSnapshot{}) {
		return snapshot, errors.Join(uptimeErr, memoryErr, storageErr)
	}
	return snapshot, nil
}
