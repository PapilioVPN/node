package grpc

// Keep the published descriptor path while the source lives with the adapter.
//go:generate protoc --proto_path=api/v1=proto/v1 --go_out=../../../.. --go_opt=module=github.com/PapilioVPN/node api/v1/host.proto

import (
	hostv1 "github.com/PapilioVPN/node/internal/host/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/host/cpu"
	"github.com/PapilioVPN/node/internal/host/memory"
	"github.com/PapilioVPN/node/internal/host/storage"
	"github.com/PapilioVPN/node/internal/host/system"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func cpuMessage(sample cpu.Sample) *hostv1.CPUSnapshot {
	message := &hostv1.CPUSnapshot{
		BusyPercent:        sample.BusyPercent,
		WindowMilliseconds: uint64(sample.Window.Milliseconds()),
		ObservedAt:         timestamppb.New(sample.ObservedAt),
	}
	if sample.HasSteal {
		message.StealPercent = &sample.StealPercent
	}
	return message
}

func memoryMessage(snapshot memory.Snapshot) *hostv1.MemorySnapshot {
	return &hostv1.MemorySnapshot{
		TotalBytes:     snapshot.TotalBytes,
		AvailableBytes: snapshot.AvailableBytes,
		SwapTotalBytes: snapshot.SwapTotalBytes,
		SwapFreeBytes:  snapshot.SwapFreeBytes,
	}
}

func storageMessage(snapshot storage.Snapshot) *hostv1.StorageSnapshot {
	return &hostv1.StorageSnapshot{
		Path:           snapshot.Path,
		TotalBytes:     snapshot.TotalBytes,
		UsedBytes:      snapshot.UsedBytes,
		AvailableBytes: snapshot.AvailableBytes,
		ReadOnly:       snapshot.ReadOnly,
	}
}

func systemMessage(snapshot system.Snapshot) *hostv1.SystemSnapshot {
	response := &hostv1.SystemSnapshot{UptimeSeconds: snapshot.UptimeSeconds}
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
