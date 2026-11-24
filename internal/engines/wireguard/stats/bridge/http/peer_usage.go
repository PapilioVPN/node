package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/accounting"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
)

type peerUsageRow struct {
	PublicKey []byte `json:"public_key"`
	RXBytes   uint64 `json:"rx_bytes,string"`
	TXBytes   uint64 `json:"tx_bytes,string"`
}

type usageSample struct {
	SampledAt  time.Time `json:"sampled_at,omitzero"`
	Incomplete bool      `json:"incomplete,omitempty"`
}

func peerUsage(service *accounting.Service, reset bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		key, err := peerKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := service.ReadPeer(r.Context(), interfaceName(r), key, reset)
		if err != nil {
			writeError(w, err)
			return
		}
		response := struct {
			usageSample
			Peer *peerUsageRow `json:"peer,omitempty"`
		}{usageSample: usageSample(result.Sample)}
		if result.Peer.Counters != (traffic.Counters{}) {
			row := toUsageRow(&result.Peer)
			response.Peer = &row
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}
}

func allPeerUsage(service *accounting.Service, reset bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		result, err := service.ReadAll(r.Context(), interfaceName(r), reset)
		if err != nil {
			writeError(w, err)
			return
		}
		response := struct {
			usageSample
			Peers []peerUsageRow `json:"peers"`
		}{usageSample: usageSample(result.Sample), Peers: make([]peerUsageRow, len(result.Peers))}
		for i := range result.Peers {
			response.Peers[i] = toUsageRow(&result.Peers[i])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}
}

func toUsageRow(row *accounting.Usage) peerUsageRow {
	return peerUsageRow{PublicKey: row.PublicKey[:], RXBytes: row.RXBytes, TXBytes: row.TXBytes}
}
