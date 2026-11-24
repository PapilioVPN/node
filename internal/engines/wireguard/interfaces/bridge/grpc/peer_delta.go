package grpc

import (
	"context"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (h *handlers) UpsertPeer(ctx context.Context, req *interfacesv1.WireGuardUpsertPeerRequest) (*interfacesv1.WireGuardPeerSyncResult, error) {
	if h.managed == nil {
		return nil, status.Error(codes.Unimplemented, "wireguard management is disabled")
	}
	return peerResult(h.managed.UpsertPeer(ctx, req.GetName(), peerInput(req.GetPeer())))
}

func (h *handlers) RemovePeer(ctx context.Context, req *interfacesv1.WireGuardRemovePeerRequest) (*interfacesv1.WireGuardRemovePeerResponse, error) {
	if h.managed == nil {
		return nil, status.Error(codes.Unimplemented, "wireguard management is disabled")
	}
	if err := h.managed.RemovePeer(ctx, req.GetName(), req.GetPublicKey()); err != nil {
		return nil, readError(err)
	}
	return &interfacesv1.WireGuardRemovePeerResponse{}, nil
}

func (h *handlers) ApplyPeerDelta(ctx context.Context, req *interfacesv1.WireGuardApplyPeerDeltaRequest) (*interfacesv1.WireGuardPeerSyncResult, error) {
	if h.managed == nil {
		return nil, status.Error(codes.Unimplemented, "wireguard management is disabled")
	}
	if len(req.GetUpsert())+len(req.GetRemove()) > peers.MaxPeers {
		return nil, readError(peers.ErrTooMany)
	}
	input := peers.DeltaInput{Upsert: make([]peers.Input, len(req.GetUpsert())), Remove: req.GetRemove()}
	for i, peer := range req.GetUpsert() {
		input.Upsert[i] = peerInput(peer)
	}
	return peerResult(h.managed.ApplyPeerDelta(ctx, req.GetName(), input))
}

func peerInput(peer *interfacesv1.WireGuardPeer) peers.Input {
	return peers.Input{PublicKey: peer.GetPublicKey(), PresharedKey: peer.GetPresharedKey(), AllowedIPs: peer.GetAllowedIps()}
}

func peerResult(result peers.Result, err error) (*interfacesv1.WireGuardPeerSyncResult, error) {
	if err != nil {
		return nil, readError(err)
	}
	return &interfacesv1.WireGuardPeerSyncResult{Added: result.Added, Updated: result.Updated, Removed: result.Removed}, nil
}
