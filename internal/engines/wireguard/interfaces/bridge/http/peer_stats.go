package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
)

type peerStatsResponse struct {
	PublicKey     []byte    `json:"public_key"`
	RXBytes       uint64    `json:"rx_bytes,string"`
	TXBytes       uint64    `json:"tx_bytes,string"`
	LastHandshake time.Time `json:"last_handshake,omitzero"`
}

func listPeerStats(service *peers.StatsService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		observations, err := service.ListPeerStats(r.Context(), interfaceName(r))
		if err != nil {
			writeError(w, err)
			return
		}
		peers := make([]peerStatsResponse, len(observations))
		for i := range observations {
			peer := &observations[i]
			peers[i] = peerStatsResponse{
				PublicKey: peer.PublicKey[:], RXBytes: peer.RXBytes, TXBytes: peer.TXBytes, LastHandshake: peer.LastHandshake,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Peers []peerStatsResponse `json:"peers"`
		}{Peers: peers})
	}
}
