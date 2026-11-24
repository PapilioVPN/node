// Package http exposes WireGuard statistics over HTTP.
package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
	"github.com/go-chi/chi/v5"
)

func Register(router chi.Router, service *traffic.Service) {
	const path = "/v1/wireguard/interfaces/{name}"
	router.Get(path+"/traffic", linkTraffic(service.GetInterfaceTraffic))
	router.Get(path+"/usage", linkTraffic(service.GetInterfaceUsage))
	router.Post(path+"/usage/resets", linkTraffic(service.ResetInterfaceUsage))
	router.Get(path+"/peers/{key}/traffic", peerTraffic(service.GetPeerTraffic))
	router.Post(path+"/peers/{key}/traffic/resets", peerTraffic(service.ResetPeerTraffic))
}

func linkTraffic(read func(context.Context, string) (traffic.Counters, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		counters, err := read(r.Context(), interfaceName(r))
		writeCounters(w, counters, err)
	}
}

func interfaceName(r *http.Request) string {
	name := chi.URLParam(r, "name")
	if r.URL.RawPath != "" {
		name, _ = url.PathUnescape(name)
	}
	return name
}

func peerTraffic(read func(context.Context, string, []byte) (traffic.Counters, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		encoded := chi.URLParam(r, "key")
		if len(encoded) != base64.RawURLEncoding.EncodedLen(32) {
			writeError(w, traffic.ErrInvalidPeerKey)
			return
		}
		key, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
		if err != nil {
			writeError(w, traffic.ErrInvalidPeerKey)
			return
		}
		counters, err := read(r.Context(), interfaceName(r), key)
		writeCounters(w, counters, err)
	}
}

func writeCounters(w http.ResponseWriter, counters traffic.Counters, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		RXBytes uint64 `json:"rx_bytes,string"`
		TXBytes uint64 `json:"tx_bytes,string"`
	}{RXBytes: counters.RXBytes, TXBytes: counters.TXBytes})
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, traffic.ErrInvalidPeerKey):
		http.Error(w, traffic.ErrInvalidPeerKey.Error(), http.StatusBadRequest)
	case errors.Is(err, traffic.ErrPeerNotFound), errors.Is(err, status.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, traffic.ErrResetCapacity), errors.Is(err, status.ErrBusy):
		http.Error(w, err.Error(), http.StatusTooManyRequests)
	case errors.Is(err, traffic.ErrResetDisabled), errors.Is(err, traffic.ErrLinkResetDisabled):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "wireguard traffic deadline exceeded", http.StatusGatewayTimeout)
	case errors.Is(err, context.Canceled):
		http.Error(w, "wireguard request canceled", http.StatusRequestTimeout)
	default:
		http.Error(w, "wireguard traffic is unavailable", http.StatusServiceUnavailable)
	}
}
