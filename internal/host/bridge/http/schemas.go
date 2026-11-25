package http

import (
	"time"

	"github.com/PapilioVPN/node/internal/host/cpu"
	"github.com/PapilioVPN/node/internal/host/system"
)

type uptimeResponse struct {
	UptimeSeconds uint64 `json:"uptime_seconds"`
}

type cpuResponse struct {
	BusyPercent        float64   `json:"busy_percent"`
	StealPercent       *float64  `json:"steal_percent,omitempty"`
	WindowMilliseconds uint64    `json:"window_milliseconds"`
	ObservedAt         time.Time `json:"observed_at"`
}

type memoryResponse struct {
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes *uint64 `json:"available_bytes,omitempty"`
	UsedBytes      *uint64 `json:"used_bytes,omitempty"`
	SwapTotalBytes uint64  `json:"swap_total_bytes"`
	SwapFreeBytes  uint64  `json:"swap_free_bytes"`
}

type storageResponse struct {
	Path           string `json:"path"`
	TotalBytes     uint64 `json:"total_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	ReadOnly       bool   `json:"read_only"`
}

type networkResponse struct {
	RXBytesPerSecond uint64    `json:"rx_bytes_per_second"`
	TXBytesPerSecond uint64    `json:"tx_bytes_per_second"`
	WindowMillis     uint64    `json:"window_milliseconds"`
	ObservedAt       time.Time `json:"observed_at"`
}

type systemResponse struct {
	UptimeSeconds *uint64          `json:"uptime_seconds,omitempty"`
	CPU           *cpuResponse     `json:"cpu,omitempty"`
	LogicalCPUs   uint32           `json:"logical_cpu_count,omitempty"`
	Network       *networkResponse `json:"network,omitempty"`
	Memory        *memoryResponse  `json:"memory,omitempty"`
	Storage       *storageResponse `json:"storage,omitempty"`
}

func cpuHTTPResponse(sample cpu.Sample) cpuResponse {
	response := cpuResponse{
		BusyPercent:        sample.BusyPercent,
		WindowMilliseconds: uint64(sample.Window.Milliseconds()),
		ObservedAt:         sample.ObservedAt.UTC(),
	}
	if sample.HasSteal {
		response.StealPercent = &sample.StealPercent
	}
	return response
}

func systemHTTPResponse(snapshot system.Snapshot) systemResponse {
	response := systemResponse{
		UptimeSeconds: snapshot.UptimeSeconds,
		LogicalCPUs:   snapshot.LogicalCPUs,
		Network:       (*networkResponse)(snapshot.Network),
		Memory:        (*memoryResponse)(snapshot.Memory),
		Storage:       (*storageResponse)(snapshot.Storage),
	}
	if snapshot.CPU != nil {
		cpu := cpuHTTPResponse(*snapshot.CPU)
		response.CPU = &cpu
	}
	return response
}
