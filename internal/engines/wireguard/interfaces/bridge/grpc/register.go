// Package grpc exposes WireGuard interface operations over gRPC.
package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/interfaces.proto

import (
	"context"
	"errors"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	interfacestatus "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type handlers struct {
	interfacesv1.UnimplementedWireGuardInterfacesServiceServer
	service   *interfacestatus.Service
	managed   *lifecycle.Service
	validator *lifecycle.Validator
}

func Register(server grpc.ServiceRegistrar, service *interfacestatus.Service, managed *lifecycle.Service, validator *lifecycle.Validator) {
	interfacesv1.RegisterWireGuardInterfacesServiceServer(server, &handlers{service: service, managed: managed, validator: validator})
}

func (h *handlers) GetStatus(ctx context.Context, req *interfacesv1.WireGuardInterfaceRequest) (*interfacesv1.WireGuardInterfaceStatus, error) {
	result, err := h.service.GetStatus(ctx, req.GetName())
	if err != nil {
		return nil, readError(err)
	}
	return response(result), nil
}

func (h *handlers) Start(ctx context.Context, req *interfacesv1.WireGuardStartRequest) (*interfacesv1.WireGuardInterfaceStatus, error) {
	if h.managed == nil {
		return nil, status.Error(codes.Unimplemented, "wireguard management is disabled")
	}
	result, err := h.managed.Start(ctx, req.GetName(), req.GetConfigJson())
	if err != nil {
		return nil, readError(err)
	}
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

func response(result interfacestatus.Interface) *interfacesv1.WireGuardInterfaceStatus {
	reply := &interfacesv1.WireGuardInterfaceStatus{Name: result.Name, Mtu: result.MTU, Up: result.Up}
	if result.AppliedSHA256 != "" {
		reply.AppliedConfigSha256 = proto.String(result.AppliedSHA256)
	}
	return reply
}

func readError(err error) error {
	switch {
	case errors.Is(err, lifecycle.ErrConflict):
		return status.Error(codes.FailedPrecondition, lifecycle.ErrConflict.Error())
	case errors.Is(err, lifecycle.ErrInvalid):
		return status.Error(codes.InvalidArgument, lifecycle.ErrInvalid.Error())
	case errors.Is(err, lifecycle.ErrTooLarge):
		return status.Error(codes.ResourceExhausted, lifecycle.ErrTooLarge.Error())
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
