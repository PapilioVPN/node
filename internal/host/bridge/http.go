package bridge

import (
	"encoding/json"
	"net/http"

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
