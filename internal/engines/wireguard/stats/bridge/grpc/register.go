// Package grpc exposes WireGuard statistics over gRPC.
package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/wireguard_stats.proto

import (
	"context"
	"errors"

	interfacestatus "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	statsv1 "github.com/PapilioVPN/node/internal/engines/wireguard/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type handlers struct {
	statsv1.UnimplementedWireGuardStatsServiceServer
	service *traffic.Service
}

func Register(server grpc.ServiceRegistrar, service *traffic.Service) {
	statsv1.RegisterWireGuardStatsServiceServer(server, &handlers{service: service})
}

func (h *handlers) GetInterfaceTraffic(ctx context.Context, req *statsv1.WireGuardInterfaceTrafficRequest) (*statsv1.WireGuardInterfaceTraffic, error) {
	counters, err := h.service.GetInterfaceTraffic(ctx, req.GetName())
	if err != nil {
		return nil, trafficError(err)
	}
	return &statsv1.WireGuardInterfaceTraffic{RxBytes: counters.RXBytes, TxBytes: counters.TXBytes}, nil
}

func (h *handlers) GetPeerTraffic(ctx context.Context, req *statsv1.WireGuardPeerTrafficRequest) (*statsv1.WireGuardPeerTraffic, error) {
	return peerResponse(h.service.GetPeerTraffic(ctx, req.GetName(), req.GetPublicKey()))
}

func (h *handlers) ResetPeerTraffic(ctx context.Context, req *statsv1.WireGuardPeerTrafficRequest) (*statsv1.WireGuardPeerTraffic, error) {
	return peerResponse(h.service.ResetPeerTraffic(ctx, req.GetName(), req.GetPublicKey()))
}

func (h *handlers) GetInterfaceUsage(ctx context.Context, req *statsv1.WireGuardInterfaceTrafficRequest) (*statsv1.WireGuardInterfaceUsage, error) {
	return usageResponse(h.service.GetInterfaceUsage(ctx, req.GetName()))
}

func (h *handlers) ResetInterfaceUsage(ctx context.Context, req *statsv1.WireGuardInterfaceTrafficRequest) (*statsv1.WireGuardInterfaceUsage, error) {
	return usageResponse(h.service.ResetInterfaceUsage(ctx, req.GetName()))
}

func usageResponse(counters traffic.Counters, err error) (*statsv1.WireGuardInterfaceUsage, error) {
	if err != nil {
		return nil, trafficError(err)
	}
	return &statsv1.WireGuardInterfaceUsage{RxBytes: counters.RXBytes, TxBytes: counters.TXBytes}, nil
}

func peerResponse(counters traffic.Counters, err error) (*statsv1.WireGuardPeerTraffic, error) {
	if err != nil {
		return nil, trafficError(err)
	}
	return &statsv1.WireGuardPeerTraffic{RxBytes: counters.RXBytes, TxBytes: counters.TXBytes}, nil
}

func trafficError(err error) error {
	switch {
	case errors.Is(err, traffic.ErrInvalidPeerKey):
		return status.Error(codes.InvalidArgument, traffic.ErrInvalidPeerKey.Error())
	case errors.Is(err, traffic.ErrPeerNotFound), errors.Is(err, interfacestatus.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, interfacestatus.ErrBusy), errors.Is(err, traffic.ErrResetCapacity):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, traffic.ErrResetDisabled), errors.Is(err, traffic.ErrLinkResetDisabled):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "wireguard request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "wireguard traffic deadline exceeded")
	default:
		return status.Error(codes.Unavailable, "wireguard traffic is unavailable")
	}
}
