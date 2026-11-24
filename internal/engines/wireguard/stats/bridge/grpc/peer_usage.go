package grpc

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/accounting"
	statsv1 "github.com/PapilioVPN/node/internal/engines/wireguard/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h *handlers) GetPeerUsage(ctx context.Context, req *statsv1.WireGuardPeerTrafficRequest) (*statsv1.WireGuardPeerUsage, error) {
	return singleUsage(h.usage.ReadPeer(ctx, req.GetName(), req.GetPublicKey(), false))
}

func (h *handlers) ResetPeerUsage(ctx context.Context, req *statsv1.WireGuardPeerTrafficRequest) (*statsv1.WireGuardPeerUsage, error) {
	return singleUsage(h.usage.ReadPeer(ctx, req.GetName(), req.GetPublicKey(), true))
}

func (h *handlers) ListPeerUsage(ctx context.Context, req *statsv1.WireGuardInterfaceTrafficRequest) (*statsv1.WireGuardPeerUsageList, error) {
	return allUsage(h.usage.ReadAll(ctx, req.GetName(), false))
}

func (h *handlers) ResetAllPeerUsage(ctx context.Context, req *statsv1.WireGuardInterfaceTrafficRequest) (*statsv1.WireGuardPeerUsageList, error) {
	return allUsage(h.usage.ReadAll(ctx, req.GetName(), true))
}

func singleUsage(result accounting.Single, err error) (*statsv1.WireGuardPeerUsage, error) {
	if err != nil {
		return nil, trafficError(err)
	}
	response := &statsv1.WireGuardPeerUsage{SampledAt: sampledAt(result.SampledAt), Incomplete: result.Incomplete}
	if result.Peer.Counters != (traffic.Counters{}) {
		response.Peer = usageRow(&result.Peer)
	}
	return response, nil
}

func allUsage(result accounting.Collection, err error) (*statsv1.WireGuardPeerUsageList, error) {
	if err != nil {
		return nil, trafficError(err)
	}
	response := &statsv1.WireGuardPeerUsageList{SampledAt: sampledAt(result.SampledAt), Incomplete: result.Incomplete}
	if len(result.Peers) > 0 {
		response.Peers = make([]*statsv1.WireGuardUsageRow, len(result.Peers))
		for i := range result.Peers {
			response.Peers[i] = usageRow(&result.Peers[i])
		}
	}
	return response, nil
}

func usageRow(row *accounting.Usage) *statsv1.WireGuardUsageRow {
	return &statsv1.WireGuardUsageRow{PublicKey: row.PublicKey[:], RxBytes: row.RXBytes, TxBytes: row.TXBytes}
}

func sampledAt(at time.Time) *timestamppb.Timestamp {
	if at.IsZero() {
		return nil
	}
	return timestamppb.New(at)
}
