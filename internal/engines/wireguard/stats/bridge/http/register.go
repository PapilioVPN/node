// Package http exposes WireGuard statistics over HTTP.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
	"github.com/go-chi/chi/v5"
)

func Register(router chi.Router, service *traffic.Service) {
	router.Get("/v1/wireguard/interfaces/{name}/traffic", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		name := chi.URLParam(r, "name")
		if r.URL.RawPath != "" {
			name, _ = url.PathUnescape(name)
		}
		counters, err := service.GetInterfaceTraffic(r.Context(), name)
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			RXBytes uint64 `json:"rx_bytes,string"`
			TXBytes uint64 `json:"tx_bytes,string"`
		}{RXBytes: counters.RXBytes, TXBytes: counters.TXBytes})
	})
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, status.ErrNotFound):
		http.Error(w, status.ErrNotFound.Error(), http.StatusNotFound)
	case errors.Is(err, status.ErrBusy):
		http.Error(w, status.ErrBusy.Error(), http.StatusTooManyRequests)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "wireguard traffic deadline exceeded", http.StatusGatewayTimeout)
	case errors.Is(err, context.Canceled):
		http.Error(w, "wireguard request canceled", http.StatusRequestTimeout)
	default:
		http.Error(w, "wireguard traffic is unavailable", http.StatusServiceUnavailable)
	}
}
