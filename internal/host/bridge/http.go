package bridge

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/PapilioVPN/node/internal/host/services"
)

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
		var steal *float64
		if sample.HasSteal {
			steal = &sample.StealPercent
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			BusyPercent        float64   `json:"busy_percent"`
			StealPercent       *float64  `json:"steal_percent,omitempty"`
			WindowMilliseconds uint64    `json:"window_milliseconds"`
			ObservedAt         time.Time `json:"observed_at"`
		}{
			BusyPercent:        sample.BusyPercent,
			StealPercent:       steal,
			WindowMilliseconds: uint64(sample.Window.Milliseconds()),
			ObservedAt:         sample.ObservedAt.UTC(),
		})
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
	_ = json.NewEncoder(w).Encode(struct {
		TotalBytes     uint64  `json:"total_bytes"`
		AvailableBytes *uint64 `json:"available_bytes,omitempty"`
		SwapTotalBytes uint64  `json:"swap_total_bytes"`
		SwapFreeBytes  uint64  `json:"swap_free_bytes"`
	}{
		TotalBytes:     snapshot.TotalBytes,
		AvailableBytes: snapshot.AvailableBytes,
		SwapTotalBytes: snapshot.SwapTotalBytes,
		SwapFreeBytes:  snapshot.SwapFreeBytes,
	})
}
