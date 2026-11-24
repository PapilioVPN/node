// Package http exposes WireGuard interface status over HTTP.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/go-chi/chi/v5"
)

type response struct {
	Name          string `json:"name"`
	MTU           uint32 `json:"mtu"`
	Up            bool   `json:"up"`
	AppliedSHA256 string `json:"applied_config_sha256,omitempty"`
}

func Register(router chi.Router, service *status.Service, managed *lifecycle.Service) {
	const path = "/v1/wireguard/interfaces/{name}"
	router.Get(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		result, err := service.GetStatus(r.Context(), interfaceName(r))
		if err != nil {
			writeError(w, err)
			return
		}
		writeStatus(w, result)
	})
	if managed == nil {
		return
	}
	router.Put(path+"/execution", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		controller := http.NewResponseController(w)
		if err := controller.SetReadDeadline(time.Now().Add(lifecycle.OperationTimeout)); err == nil {
			defer controller.SetReadDeadline(time.Time{})
		}
		source, err := io.ReadAll(http.MaxBytesReader(w, r.Body, lifecycle.MaxConfigBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, lifecycle.ErrTooLarge)
				return
			}
			writeError(w, lifecycle.ErrInvalid)
			return
		}
		result, err := managed.Start(r.Context(), interfaceName(r), source)
		if err != nil {
			writeError(w, err)
			return
		}
		writeStatus(w, result)
	})
	router.Delete(path+"/execution", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if err := managed.Stop(r.Context(), interfaceName(r)); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func interfaceName(r *http.Request) string {
	name := chi.URLParam(r, "name")
	if r.URL.RawPath != "" {
		name, _ = url.PathUnescape(name)
	}
	return name
}

func writeStatus(w http.ResponseWriter, link status.Interface) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response{
		Name: link.Name, MTU: link.MTU, Up: link.Up, AppliedSHA256: link.AppliedSHA256,
	})
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lifecycle.ErrConflict):
		http.Error(w, lifecycle.ErrConflict.Error(), http.StatusConflict)
	case errors.Is(err, lifecycle.ErrInvalid):
		http.Error(w, lifecycle.ErrInvalid.Error(), http.StatusBadRequest)
	case errors.Is(err, lifecycle.ErrTooLarge):
		http.Error(w, lifecycle.ErrTooLarge.Error(), http.StatusRequestEntityTooLarge)
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
