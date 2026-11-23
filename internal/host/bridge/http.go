package bridge

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/PapilioVPN/node/internal/host/services"
)

type cpuResponse struct {
	BusyPercent        float64   `json:"busy_percent"`
	StealPercent       *float64  `json:"steal_percent,omitempty"`
	WindowMilliseconds uint64    `json:"window_milliseconds"`
	ObservedAt         time.Time `json:"observed_at"`
}

type memoryResponse struct {
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes *uint64 `json:"available_bytes,omitempty"`
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

func handleUptime(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	seconds, err := services.Uptime(request.Context())
	if err != nil {
		http.Error(w, "host uptime is unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		UptimeSeconds uint64 `json:"uptime_seconds"`
	}{UptimeSeconds: seconds})
}

func handleCPU(sampler *services.CPUSampler) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		sample, ok := sampler.Latest()
		if !ok {
			http.Error(w, "host CPU sample is unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cpuHTTPResponse(sample))
	}
}

func handleMemory(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	snapshot, err := services.Memory(request.Context())
	if err != nil {
		http.Error(w, "host memory is unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(memoryResponse(snapshot))
}

func handleStorage(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		snapshot, err := services.Storage(request.Context(), path)
		if err != nil {
			http.Error(w, "host storage is unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(storageResponse(snapshot))
	}
}

func handleSystemStats(sampler *services.CPUSampler, storagePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		snapshot, err := services.SystemStats(request.Context(), sampler, storagePath)
		if err != nil {
			http.Error(w, "host system stats are unavailable", http.StatusServiceUnavailable)
			return
		}
		response := struct {
			UptimeSeconds *uint64          `json:"uptime_seconds,omitempty"`
			CPU           *cpuResponse     `json:"cpu,omitempty"`
			Memory        *memoryResponse  `json:"memory,omitempty"`
			Storage       *storageResponse `json:"storage,omitempty"`
		}{
			UptimeSeconds: snapshot.UptimeSeconds,
			Memory:        (*memoryResponse)(snapshot.Memory),
			Storage:       (*storageResponse)(snapshot.Storage),
		}
		if snapshot.CPU != nil {
			cpu := cpuHTTPResponse(*snapshot.CPU)
			response.CPU = &cpu
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}
}

func cpuHTTPResponse(sample services.CPUSample) cpuResponse {
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
