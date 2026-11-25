package grpc

import (
	"context"

	statsv1 "github.com/PapilioVPN/node/internal/engines/wireguard/stats/bridge/grpc/proto/v1"
)

func (h *handlers) GetPeerActivity(ctx context.Context, req *statsv1.WireGuardPeerTrafficRequest) (*statsv1.WireGuardPeerActivity, error) {
	activity, err := h.usage.PeerActivity(ctx, req.GetName(), req.GetPublicKey())
	if err != nil {
		return nil, trafficError(err)
	}
	return &statsv1.WireGuardPeerActivity{Active: activity.Active, LastActiveAt: sampledAt(activity.LastActive)}, nil
}

func (h *handlers) ListPeerEndpointIPs(ctx context.Context, req *statsv1.WireGuardPeerTrafficRequest) (*statsv1.WireGuardPeerEndpointIPs, error) {
	activity, err := h.usage.PeerActivity(ctx, req.GetName(), req.GetPublicKey())
	if err != nil {
		return nil, trafficError(err)
	}
	result := &statsv1.WireGuardPeerEndpointIPs{}
	if activity.Endpoint.IsValid() {
		result.Endpoints = []*statsv1.WireGuardPeerEndpointIP{{Ip: activity.Endpoint.String(), LastActiveAt: sampledAt(activity.LastActive)}}
	}
	return result, nil
}
