package http

import (
	"encoding/json"
	"net/http"

	"github.com/PapilioVPN/node/internal/host/cpu"
	"github.com/PapilioVPN/node/internal/host/memory"
	"github.com/PapilioVPN/node/internal/host/network"
	"github.com/PapilioVPN/node/internal/host/storage"
	"github.com/PapilioVPN/node/internal/host/system"
)

func handleUptime(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	seconds, err := system.Uptime(request.Context())
	if err != nil {
		http.Error(w, "host uptime is unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(uptimeResponse{UptimeSeconds: seconds})
}

func handleCPU(sampler *cpu.Sampler) http.HandlerFunc {
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
	snapshot, err := memory.Read(request.Context())
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
		snapshot, err := storage.Read(request.Context(), path)
		if err != nil {
			http.Error(w, "host storage is unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(storageResponse(snapshot))
	}
}

func handleSystemStats(sampler *cpu.Sampler, netSampler *network.Sampler, storagePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		snapshot, err := system.Stats(request.Context(), sampler, netSampler, storagePath)
		if err != nil {
			http.Error(w, "host system stats are unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(systemHTTPResponse(snapshot))
	}
}
