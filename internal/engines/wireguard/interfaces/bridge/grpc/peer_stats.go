package grpc

import (
	"context"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h *handlers) ListPeerStats(ctx context.Context, req *interfacesv1.WireGuardInterfaceRequest) (*interfacesv1.WireGuardPeerStatsList, error) {
	observations, err := h.peerStats.ListPeerStats(ctx, req.GetName())
	if err != nil {
		return nil, readError(err)
	}
	result := &interfacesv1.WireGuardPeerStatsList{Peers: make([]*interfacesv1.WireGuardPeerStats, len(observations))}
	for i := range observations {
		observed := &observations[i]
		peer := &interfacesv1.WireGuardPeerStats{PublicKey: observed.PublicKey[:], RxBytes: observed.RXBytes, TxBytes: observed.TXBytes}
		if !observed.LastHandshake.IsZero() {
			peer.LastHandshake = timestamppb.New(observed.LastHandshake)
		}
		result.Peers[i] = peer
	}
	return result, nil
}
