package host

import (
	"encoding/json"
	"net/http"
)

func handleUptime(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	seconds, err := readUptime(request.Context())
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
	snapshot, err := readMemory(request.Context())
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
		TotalBytes:     snapshot.totalBytes,
		AvailableBytes: snapshot.availableBytes,
		SwapTotalBytes: snapshot.swapTotalBytes,
		SwapFreeBytes:  snapshot.swapFreeBytes,
	})
}
