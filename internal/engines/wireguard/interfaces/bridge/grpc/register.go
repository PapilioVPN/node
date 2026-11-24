// Package grpc exposes WireGuard interface status over gRPC.
package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/interfaces.proto

import (
	"context"
	"errors"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	interfacestatus "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type handlers struct {
	interfacesv1.UnimplementedWireGuardInterfacesServiceServer
	service *interfacestatus.Service
}

func Register(server grpc.ServiceRegistrar, service *interfacestatus.Service) {
	interfacesv1.RegisterWireGuardInterfacesServiceServer(server, &handlers{service: service})
}

func (h *handlers) GetStatus(ctx context.Context, req *interfacesv1.WireGuardInterfaceRequest) (*interfacesv1.WireGuardInterfaceStatus, error) {
	result, err := h.service.GetStatus(ctx, req.GetName())
	if err != nil {
		return nil, readError(err)
	}
	return &interfacesv1.WireGuardInterfaceStatus{Name: result.Name, Mtu: result.MTU, Up: result.Up}, nil
}

func readError(err error) error {
	switch {
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
