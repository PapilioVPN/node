package grpc

import (
	"context"
	"errors"
	"io"
	"slices"
	"time"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (h *handlers) UpsertPeer(ctx context.Context, req *interfacesv1.WireGuardUpsertPeerRequest) (*interfacesv1.WireGuardPeerSyncResult, error) {
	if h.managed == nil {
		return nil, status.Error(codes.Unimplemented, "wireguard management is disabled")
	}
	return peerResult(h.managed.UpsertPeer(ctx, req.GetName(), peerInput(req.GetPeer())))
}

func (h *handlers) ApplyPeerDeltaStream(stream grpc.ClientStreamingServer[interfacesv1.WireGuardPeerDeltaChunk, interfacesv1.WireGuardPeerSyncResult]) error {
	if h.managed == nil {
		return status.Error(codes.Unimplemented, "wireguard management is disabled")
	}
	ctx := stream.Context()
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > lifecycle.PeerDeltaStreamTimeout {
		return status.Error(codes.InvalidArgument, "wireguard delta stream requires a deadline of at most 30 seconds")
	}
	var decoded []peers.Input
	result, err := h.managed.ApplyPeerDeltaChunks(ctx, func() (lifecycle.PeerDeltaChunk, error) {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return lifecycle.PeerDeltaChunk{}, io.EOF
		}
		if err != nil {
			return lifecycle.PeerDeltaChunk{}, status.Convert(err).Err()
		}
		if len(chunk.GetUpsert())+len(chunk.GetRemove()) > peers.MaxPeers {
			return lifecycle.PeerDeltaChunk{}, lifecycle.ErrPeerDeltaStreamSize
		}
		decoded = slices.Grow(decoded[:0], len(chunk.GetUpsert()))[:len(chunk.GetUpsert())]
		for i, peer := range chunk.GetUpsert() {
			decoded[i] = peerInput(peer)
		}
		return lifecycle.PeerDeltaChunk{
			DeltaInput: peers.DeltaInput{Upsert: decoded, Remove: chunk.GetRemove()},
			Index:      chunk.GetIndex(), Last: chunk.GetLast(), Name: chunk.GetName(), Bytes: proto.Size(chunk),
		}, nil
	})
	switch {
	case errors.Is(err, lifecycle.ErrPeerDeltaStreamBusy), errors.Is(err, lifecycle.ErrPeerDeltaStreamSize):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	}
	if _, ok := status.FromError(err); err != nil && ok {
		return err
	}
	reply, err := peerResult(result, err)
	if err != nil {
		return err
	}
	return stream.SendAndClose(reply)
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
