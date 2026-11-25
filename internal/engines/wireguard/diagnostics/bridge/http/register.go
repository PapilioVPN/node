package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/events"
	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/process"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/go-chi/chi/v5"
)

func Register(router chi.Router, logs *events.Service, runtime *process.Runtime) {
	const path = "/v1/wireguard/interfaces/{name}"
	router.Get(path+"/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		sub, err := logs.Subscribe(r.Context(), interfaceName(r))
		if err != nil {
			writeError(w, err)
			return
		}
		defer sub.Close()
		w.Header().Set("Content-Type", "application/x-ndjson")
		control := http.NewResponseController(w)
		if control.SetWriteDeadline(time.Now().Add(2*time.Second)) != nil || control.Flush() != nil || control.SetWriteDeadline(time.Time{}) != nil {
			return
		}
		encoder := json.NewEncoder(w)
		for {
			event, err := sub.Next()
			if err != nil || control.SetWriteDeadline(time.Now().Add(2*time.Second)) != nil {
				return
			}
			if encoder.Encode(event) != nil || control.Flush() != nil || control.SetWriteDeadline(time.Time{}) != nil {
				return
			}
		}
	})
	router.Get(path+"/runtime", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		result, err := runtime.Read(r.Context(), interfaceName(r))
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Goroutines         int     `json:"goroutines"`
			GCCount            uint32  `json:"gc_count"`
			HeapBytes          uint64  `json:"heap_bytes,string"`
			AllocatedBytes     uint64  `json:"allocated_bytes,string"`
			SystemBytes        uint64  `json:"system_bytes,string"`
			Allocations        uint64  `json:"allocations,string"`
			Frees              uint64  `json:"frees,string"`
			LiveObjects        uint64  `json:"live_objects,string"`
			GCPauseNanoseconds uint64  `json:"gc_pause_nanoseconds,string"`
			UptimeSeconds      *uint64 `json:"uptime_seconds,omitempty,string"`
		}{result.Goroutines, result.GCCount, result.HeapBytes, result.AllocatedBytes, result.SystemBytes, result.Allocations,
			result.Frees, result.LiveObjects, result.GCPauseNanoseconds, result.Uptime()})
	})
}

func interfaceName(r *http.Request) string {
	name := chi.URLParam(r, "name")
	if r.URL.RawPath != "" {
		name, _ = url.PathUnescape(name)
	}
	return name
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, status.ErrNotFound):
		http.Error(w, status.ErrNotFound.Error(), http.StatusNotFound)
	case errors.Is(err, events.ErrCapacity), errors.Is(err, status.ErrBusy):
		http.Error(w, err.Error(), http.StatusTooManyRequests)
	default:
		http.Error(w, "wireguard diagnostics unavailable", http.StatusServiceUnavailable)
	}
}
