package http

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/accounting"
)

type observedEndpoint struct {
	IP           netip.Addr `json:"ip"`
	LastActiveAt time.Time  `json:"last_active_at"`
}

func peerActivity(service *accounting.Service, endpoints bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		key, err := peerKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := service.PeerActivity(r.Context(), interfaceName(r), key)
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		if endpoints {
			ips := []observedEndpoint{}
			if result.Endpoint.IsValid() {
				ips = append(ips, observedEndpoint{IP: result.Endpoint, LastActiveAt: result.LastActive})
			}
			_ = encoder.Encode(struct {
				Endpoints []observedEndpoint `json:"endpoints"`
			}{Endpoints: ips})
			return
		}
		_ = encoder.Encode(struct {
			Active       bool      `json:"active"`
			LastActiveAt time.Time `json:"last_active_at,omitzero"`
		}{Active: result.Active, LastActiveAt: result.LastActive})
	}
}
