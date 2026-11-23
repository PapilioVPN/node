package grpc

import (
	"context"
	"errors"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type handlers struct {
	accountsv1.UnimplementedXrayAccountsServiceServer
	service *membership.Service
}

func Register(server grpc.ServiceRegistrar, service *membership.Service) {
	accountsv1.RegisterXrayAccountsServiceServer(server, &handlers{service: service})
}

func (h *handlers) AddVLESSAccount(ctx context.Context, req *accountsv1.AddVLESSAccountRequest) (*emptypb.Empty, error) {
	err := h.service.Add(ctx, membership.AddRequest{
		Scope: membership.Scope{InstanceID: req.GetId(), InboundTag: req.GetInboundTag()},
		Email: req.GetEmail(),
		UUID:  req.GetUuid(),
	})
	if err != nil {
		return nil, accountError(err)
	}
	return &emptypb.Empty{}, nil
}

func (h *handlers) RemoveVLESSAccount(ctx context.Context, req *accountsv1.RemoveVLESSAccountRequest) (*emptypb.Empty, error) {
	err := h.service.Remove(ctx, membership.RemoveRequest{
		Scope: membership.Scope{InstanceID: req.GetId(), InboundTag: req.GetInboundTag()},
		Email: req.GetEmail(),
	})
	if err != nil {
		return nil, accountError(err)
	}
	return &emptypb.Empty{}, nil
}

func (h *handlers) CountVLESSAccounts(ctx context.Context, req *accountsv1.CountVLESSAccountsRequest) (*accountsv1.CountVLESSAccountsResponse, error) {
	count, err := h.service.Count(ctx, membership.Scope{InstanceID: req.GetId(), InboundTag: req.GetInboundTag()})
	if err != nil {
		return nil, accountError(err)
	}
	return &accountsv1.CountVLESSAccountsResponse{Count: count}, nil
}

func accountError(err error) error {
	switch {
	case errors.Is(err, membership.ErrInvalid):
		return status.Error(codes.InvalidArgument, membership.ErrInvalid.Error())
	case errors.Is(err, membership.ErrInstanceMissing), errors.Is(err, membership.ErrInboundMissing), errors.Is(err, membership.ErrAccountMissing):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, membership.ErrProtocol), errors.Is(err, membership.ErrAmbiguous), errors.Is(err, membership.ErrFenced):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, membership.ErrExists):
		return status.Error(codes.AlreadyExists, membership.ErrExists.Error())
	case errors.Is(err, membership.ErrUnknownEffect):
		return status.Error(codes.Aborted, membership.ErrUnknownEffect.Error())
	case errors.Is(err, membership.ErrBusy), errors.Is(err, membership.ErrTooLarge):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	default:
		return status.Error(codes.Unavailable, membership.ErrUnavailable.Error())
	}
}
