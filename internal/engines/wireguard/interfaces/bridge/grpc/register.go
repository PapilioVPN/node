// Package grpc exposes WireGuard interface operations over gRPC.
package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/interfaces.proto

import (
	"context"
	"errors"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	interfacestatus "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type handlers struct {
	interfacesv1.UnimplementedWireGuardInterfacesServiceServer
	service    *interfacestatus.Service
	peerStats  *traffic.Service
	managed    *lifecycle.Service
	validator  *lifecycle.Validator
	membership *peers.Membership
}

func Register(server grpc.ServiceRegistrar, service *interfacestatus.Service, peerStats *traffic.Service, managed *lifecycle.Service, validator *lifecycle.Validator, membership *peers.Membership) {
	interfacesv1.RegisterWireGuardInterfacesServiceServer(server, &handlers{
		service: service, peerStats: peerStats, managed: managed, validator: validator, membership: membership,
	})
}

func (h *handlers) ListPeers(ctx context.Context, req *interfacesv1.WireGuardInterfaceRequest) (*interfacesv1.WireGuardPeerMembership, error) {
	observed, err := h.membership.List(ctx, req.GetName())
	if err != nil {
		return nil, readError(err)
	}
	result := &interfacesv1.WireGuardPeerMembership{Peers: make([]*interfacesv1.WireGuardPeerMember, len(observed))}
	for i := range observed {
		peer := &observed[i]
		ips := make([]string, len(peer.AllowedIPs))
		for j, prefix := range peer.AllowedIPs {
			ips[j] = prefix.String()
		}
		result.Peers[i] = &interfacesv1.WireGuardPeerMember{PublicKey: peer.PublicKey[:], AllowedIps: ips}
	}
	return result, nil
}

func (h *handlers) GetStatus(ctx context.Context, req *interfacesv1.WireGuardInterfaceRequest) (*interfacesv1.WireGuardInterfaceStatus, error) {
	result, err := h.service.GetStatus(ctx, req.GetName(), req.GetIncludeConfiguration())
	if err != nil {
		return nil, readError(err)
	}
	return response(result), nil
}

func (h *handlers) Start(ctx context.Context, req *interfacesv1.WireGuardStartRequest) (*interfacesv1.WireGuardInterfaceStatus, error) {
	if h.managed == nil {
		return nil, status.Error(codes.Unimplemented, "wireguard management is disabled")
	}
	var seconds *uint32
	if req != nil {
		seconds = req.KeepAliveSeconds
	}
	result, err := h.managed.StartWithLease(ctx, req.GetName(), req.GetConfigJson(), seconds)
	if err != nil {
		return nil, readError(err)
	}
	result.Versions = h.service.Versions()
	return response(result), nil
}

func (h *handlers) Stop(ctx context.Context, req *interfacesv1.WireGuardInterfaceRequest) (*interfacesv1.WireGuardStopResponse, error) {
	if h.managed == nil {
		return nil, status.Error(codes.Unimplemented, "wireguard management is disabled")
	}
	if err := h.managed.Stop(ctx, req.GetName()); err != nil {
		return nil, readError(err)
	}
	return &interfacesv1.WireGuardStopResponse{}, nil
}

func (h *handlers) ValidateConfig(ctx context.Context, req *interfacesv1.WireGuardValidateConfigRequest) (*interfacesv1.WireGuardConfigValidation, error) {
	issue, err := h.validator.Check(ctx, req.GetName(), req.GetConfigJson())
	if err != nil {
		return nil, readError(err)
	}
	return &interfacesv1.WireGuardConfigValidation{
		Valid: issue == lifecycle.ConfigIssueNone,
		Issue: interfacesv1.WireGuardConfigIssue(issue),
	}, nil
}

func (h *handlers) SyncPeers(ctx context.Context, req *interfacesv1.WireGuardSyncPeersRequest) (*interfacesv1.WireGuardPeerSyncResult, error) {
	if h.managed == nil {
		return nil, status.Error(codes.Unimplemented, "wireguard management is disabled")
	}
	desired := req.GetDesired()
	if desired == nil {
		return nil, readError(peers.ErrInvalid)
	}
	if len(desired.GetPeers()) > peers.MaxPeers {
		return nil, readError(peers.ErrTooMany)
	}
	input := make([]peers.Input, len(desired.GetPeers()))
	for i, peer := range desired.GetPeers() {
		input[i] = peerInput(peer)
	}
	return peerResult(h.managed.SyncPeers(ctx, req.GetName(), input, req.GetReconcile()))
}

func response(result interfacestatus.Interface) *interfacesv1.WireGuardInterfaceStatus {
	reply := &interfacesv1.WireGuardInterfaceStatus{Name: result.Name, Mtu: result.MTU, Up: result.Up}
	reply.NodeVersion, reply.KernelRelease, reply.WireguardToolsVersion = result.Versions.Node, result.Versions.Kernel, result.Versions.Tools
	reply.Recreated = result.Recreated
	reply.InterfaceIndex = uint32(result.Index)
	if !result.KeepAliveExpiresAt.IsZero() {
		reply.KeepAliveExpiresAt = timestamppb.New(result.KeepAliveExpiresAt)
	}
	if result.AppliedSHA256 != "" {
		reply.AppliedConfigSha256 = proto.String(result.AppliedSHA256)
	}
	if current := result.Configuration; current != nil {
		configuration := &interfacesv1.WireGuardInterfaceConfiguration{
			ListenPort: uint32(current.ListenPort),
			Addresses:  make([]string, len(current.Addresses)),
		}
		if current.PublicKey != [32]byte{} {
			configuration.PublicKey = current.PublicKey[:]
		}
		for i, prefix := range current.Addresses {
			configuration.Addresses[i] = prefix.String()
		}
		reply.Configuration = configuration
	}
	return reply
}

func readError(err error) error {
	switch {
	case errors.Is(err, peers.ErrInvalid):
		return status.Error(codes.InvalidArgument, peers.ErrInvalid.Error())
	case errors.Is(err, peers.ErrTooMany):
		return status.Error(codes.ResourceExhausted, peers.ErrTooMany.Error())
	case errors.Is(err, peers.ErrUnconfirmed):
		return status.Error(codes.Unavailable, peers.ErrUnconfirmed.Error())
	case errors.Is(err, peers.ErrPrefixConflict), errors.Is(err, peers.ErrCapacity):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, lifecycle.ErrConflict):
		return status.Error(codes.FailedPrecondition, lifecycle.ErrConflict.Error())
	case errors.Is(err, lifecycle.ErrInvalid):
		return status.Error(codes.InvalidArgument, lifecycle.ErrInvalid.Error())
	case errors.Is(err, lifecycle.ErrTooLarge):
		return status.Error(codes.ResourceExhausted, lifecycle.ErrTooLarge.Error())
	case errors.Is(err, lifecycle.ErrUnconfirmed):
		return status.Error(codes.Unavailable, lifecycle.ErrUnconfirmed.Error())
	case errors.Is(err, interfacestatus.ErrNotFound):
		return status.Error(codes.NotFound, interfacestatus.ErrNotFound.Error())
	case errors.Is(err, interfacestatus.ErrBusy):
		return status.Error(codes.ResourceExhausted, interfacestatus.ErrBusy.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "wireguard request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "wireguard status deadline exceeded")
	default:
		return status.Error(codes.Unavailable, interfacestatus.ErrUnavailable.Error())
	}
}
