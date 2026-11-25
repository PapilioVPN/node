package http

import (
	"encoding/json"
	"net/http"
	"net/netip"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
)

type peerMemberResponse struct {
	PublicKey  []byte         `json:"public_key"`
	AllowedIPs []netip.Prefix `json:"allowed_ips"`
}

func listPeers(service *peers.Membership) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		observed, err := service.List(r.Context(), interfaceName(r))
		if err != nil {
			writeError(w, err)
			return
		}
		response := make([]peerMemberResponse, len(observed))
		for i := range observed {
			peer := &observed[i]
			response[i] = peerMemberResponse{PublicKey: peer.PublicKey[:], AllowedIPs: peer.AllowedIPs}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Peers []peerMemberResponse `json:"peers"`
		}{Peers: response})
	}
}
