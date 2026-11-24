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
	switch {
	case err == nil:
		return &statsv1.WireGuardInterfaceTraffic{RxBytes: counters.RXBytes, TxBytes: counters.TXBytes}, nil
	case errors.Is(err, interfacestatus.ErrNotFound):
		return nil, status.Error(codes.NotFound, interfacestatus.ErrNotFound.Error())
	case errors.Is(err, interfacestatus.ErrBusy):
		return nil, status.Error(codes.ResourceExhausted, interfacestatus.ErrBusy.Error())
	case errors.Is(err, context.Canceled):
		return nil, status.Error(codes.Canceled, "wireguard request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return nil, status.Error(codes.DeadlineExceeded, "wireguard traffic deadline exceeded")
	default:
		return nil, status.Error(codes.Unavailable, "wireguard traffic is unavailable")
	}
}
