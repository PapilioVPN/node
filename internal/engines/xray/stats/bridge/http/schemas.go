package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/runtime"
)

type runtimeStatsResponse struct {
	Goroutines              uint32 `json:"goroutines"`
	GCCount                 uint32 `json:"gc_count"`
	HeapAllocatedBytes      uint64 `json:"heap_allocated_bytes"`
	TotalAllocatedBytes     uint64 `json:"total_allocated_bytes"`
	SystemBytes             uint64 `json:"system_bytes"`
	Mallocs                 uint64 `json:"mallocs"`
	Frees                   uint64 `json:"frees"`
	LiveObjects             uint64 `json:"live_objects"`
	GCPauseTotalNanoseconds uint64 `json:"gc_pause_total_nanoseconds"`
	UptimeSeconds           uint32 `json:"uptime_seconds"`
}

func writeResult(w http.ResponseWriter, result runtime.Snapshot, err error) {
	if err != nil {
		switch {
		case errors.Is(err, lifecycle.ErrNotFound):
			http.Error(w, lifecycle.ErrNotFound.Error(), http.StatusNotFound)
		case errors.Is(err, runtime.ErrBusy):
			http.Error(w, runtime.ErrBusy.Error(), http.StatusTooManyRequests)
		case errors.Is(err, context.Canceled):
			http.Error(w, "xray statistics request canceled", http.StatusRequestTimeout)
		case errors.Is(err, context.DeadlineExceeded):
			http.Error(w, "xray statistics request deadline exceeded", http.StatusGatewayTimeout)
		default:
			http.Error(w, runtime.ErrUnavailable.Error(), http.StatusServiceUnavailable)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(runtimeStatsResponse(result))
}
