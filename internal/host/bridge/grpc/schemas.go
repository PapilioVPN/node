package grpc

// Keep the published descriptor path while the source lives with the adapter.
//go:generate protoc --proto_path=api/v1=proto/v1 --go_out=../../../.. --go_opt=module=github.com/PapilioVPN/node api/v1/host.proto

import (
	nodev1 "github.com/PapilioVPN/node/api/v1"
	"github.com/PapilioVPN/node/internal/host/services"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func cpuMessage(sample services.CPUSample) *nodev1.CPUSnapshot {
	message := &nodev1.CPUSnapshot{
		BusyPercent:        sample.BusyPercent,
		WindowMilliseconds: uint64(sample.Window.Milliseconds()),
		ObservedAt:         timestamppb.New(sample.ObservedAt),
	}
	if sample.HasSteal {
		message.StealPercent = &sample.StealPercent
	}
	return message
}

func memoryMessage(snapshot services.MemorySnapshot) *nodev1.MemorySnapshot {
	return &nodev1.MemorySnapshot{
		TotalBytes:     snapshot.TotalBytes,
		AvailableBytes: snapshot.AvailableBytes,
		SwapTotalBytes: snapshot.SwapTotalBytes,
		SwapFreeBytes:  snapshot.SwapFreeBytes,
	}
}

func storageMessage(snapshot services.StorageSnapshot) *nodev1.StorageSnapshot {
	return &nodev1.StorageSnapshot{
		Path:           snapshot.Path,
		TotalBytes:     snapshot.TotalBytes,
		UsedBytes:      snapshot.UsedBytes,
		AvailableBytes: snapshot.AvailableBytes,
		ReadOnly:       snapshot.ReadOnly,
	}
}

func systemMessage(snapshot services.SystemSnapshot) *nodev1.SystemSnapshot {
	response := &nodev1.SystemSnapshot{UptimeSeconds: snapshot.UptimeSeconds}
	if snapshot.CPU != nil {
		response.Cpu = cpuMessage(*snapshot.CPU)
	}
	if snapshot.Memory != nil {
		response.Memory = memoryMessage(*snapshot.Memory)
	}
	if snapshot.Storage != nil {
		response.Storage = storageMessage(*snapshot.Storage)
	}
	return response
}
