package grpc

import (
	"context"
	"errors"
	"io"
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
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
		return status.Error(codes.InvalidArgument, "wireguard delta stream requires a deadline of at most 30 seconds")
	}
	if !h.collecting.CompareAndSwap(false, true) {
		return status.Error(codes.ResourceExhausted, "wireguard delta stream is busy")
	}
	defer h.collecting.Store(false)
	var input peers.DeltaInput
	var name string
	var next uint32
	var size int
	var last bool
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		count := len(chunk.GetUpsert()) + len(chunk.GetRemove())
		if last || chunk.GetIndex() != next || chunk.GetName() == "" || count == 0 {
			return readError(peers.ErrInvalid)
		}
		if next == 0 {
			name = chunk.GetName()
		} else if chunk.GetName() != name {
			return readError(peers.ErrInvalid)
		}
		size += proto.Size(chunk)
		if size > lifecycle.MaxConfigBytes || len(input.Upsert)+len(input.Remove)+count > peers.MaxPeers {
			return status.Error(codes.ResourceExhausted, "wireguard delta stream exceeds its size limit")
		}
		for _, peer := range chunk.GetUpsert() {
			input.Upsert = append(input.Upsert, peerInput(peer))
		}
		input.Remove = append(input.Remove, chunk.GetRemove()...)
		next++
		last = chunk.GetLast()
	}
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	if !last {
		return readError(peers.ErrInvalid)
	}
	result, err := peerResult(h.managed.ApplyPeerDelta(ctx, name, input))
	if err != nil {
		return err
	}
	return stream.SendAndClose(result)
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
