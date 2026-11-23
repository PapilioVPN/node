package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"github.com/go-chi/chi/v5"
)

type trafficUsage struct {
	Name          string `json:"name"`
	UplinkBytes   *int64 `json:"uplink_bytes,omitempty"`
	DownlinkBytes *int64 `json:"downlink_bytes,omitempty"`
}

func usage(entry *traffic.Usage) trafficUsage {
	result := trafficUsage{Name: entry.Name}
	if entry.HasUplink {
		result.UplinkBytes = &entry.UplinkBytes
	}
	if entry.HasDownlink {
		result.DownlinkBytes = &entry.DownlinkBytes
	}
	return result
}

func RegisterTraffic(router chi.Router, service *traffic.Service) {
	router.Get("/v1/xray/instances/{id}/stats/traffic/{scope}", getTraffic(service))
	router.Post("/v1/xray/instances/{id}/stats/traffic/{scope}/resets", resetTraffic(service))
}

func getTraffic(service *traffic.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		scope := traffic.Scope(chi.URLParam(r, "scope"))
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values["name"]) > 1 {
			writeTrafficError(w, traffic.ErrInvalid)
			return
		}
		if values.Has("name") {
			result, err := service.Get(r.Context(), id, scope, values.Get("name"))
			if err != nil {
				writeTrafficError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(usage(&result))
			return
		}
		results, err := service.List(r.Context(), id, scope)
		if err != nil {
			writeTrafficError(w, err)
			return
		}
		response := struct {
			Entries []trafficUsage `json:"entries"`
		}{Entries: make([]trafficUsage, len(results))}
		for i := range results {
			response.Entries[i] = usage(&results[i])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}
}

func writeTrafficError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lifecycle.ErrNotFound):
		http.Error(w, lifecycle.ErrNotFound.Error(), http.StatusNotFound)
	case errors.Is(err, traffic.ErrNotFound):
		http.Error(w, traffic.ErrNotFound.Error(), http.StatusNotFound)
	case errors.Is(err, traffic.ErrInvalid):
		http.Error(w, traffic.ErrInvalid.Error(), http.StatusBadRequest)
	case errors.Is(err, traffic.ErrBusy):
		http.Error(w, traffic.ErrBusy.Error(), http.StatusTooManyRequests)
	case errors.Is(err, traffic.ErrTooLarge):
		http.Error(w, traffic.ErrTooLarge.Error(), http.StatusUnprocessableEntity)
	case errors.Is(err, context.Canceled):
		http.Error(w, "Xray traffic request canceled", http.StatusRequestTimeout)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "Xray traffic request deadline exceeded", http.StatusGatewayTimeout)
	default:
		http.Error(w, "Xray traffic counters unavailable", http.StatusServiceUnavailable)
	}
}
