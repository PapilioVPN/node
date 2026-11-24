// Package http exposes WireGuard interface status over HTTP.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/go-chi/chi/v5"
)

type response struct {
	Name string `json:"name"`
	MTU  uint32 `json:"mtu"`
	Up   bool   `json:"up"`
}

func Register(router chi.Router, service *status.Service) {
	router.Get("/v1/wireguard/interfaces/{name}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		name := chi.URLParam(r, "name")
		if r.URL.RawPath != "" {
			var err error
			name, err = url.PathUnescape(name)
			if err != nil {
				writeError(w, status.ErrNotFound)
				return
			}
		}
		result, err := service.GetStatus(r.Context(), name)
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response(result))
	})
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, status.ErrNotFound):
		http.Error(w, status.ErrNotFound.Error(), http.StatusNotFound)
	case errors.Is(err, status.ErrBusy):
		http.Error(w, status.ErrBusy.Error(), http.StatusTooManyRequests)
	case errors.Is(err, context.Canceled):
		http.Error(w, "wireguard request canceled", http.StatusRequestTimeout)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "wireguard status deadline exceeded", http.StatusGatewayTimeout)
	default:
		http.Error(w, status.ErrUnavailable.Error(), http.StatusServiceUnavailable)
	}
}
