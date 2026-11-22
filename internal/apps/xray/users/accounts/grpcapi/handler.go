// Package grpcapi exposes native account observations through a versioned API.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	v1.UnimplementedAccountsServiceServer
	Configurations *services.Configurations
	Service        *services.Service
	Sources        *services.Sources
	Timeout        time.Duration
}

func (h Handler) Register(registrar grpc.ServiceRegistrar) {
	v1.RegisterAccountsServiceServer(registrar, h)
}
func (h Handler) GetAccountCount(ctx context.Context, request *v1.GetAccountCountRequest) (*v1.AccountCount, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Count(ctx, request.InstanceId, request.InboundTag)
	if err != nil {
		return nil, failure(err)
	}
	return &v1.AccountCount{Observation: metadata(result.Observation), Count: result.Count}, nil
}
func (h Handler) GetAccount(ctx context.Context, request *v1.GetAccountRequest) (*v1.AccountObservation, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Get(ctx, request.InstanceId, request.InboundTag, request.Email)
	if err != nil {
		return nil, failure(err)
	}
	response := &v1.AccountObservation{Observation: metadata(result.Observation), RequestedEmail: result.RequestedEmail}
	if result.User != nil {
		response.User = &v1.Account{Email: result.User.Email, Level: result.User.Level, Configuration: result.User.Configuration}
	}
	return response, nil
}
func metadata(result domain.Observation) *v1.Observation {
	return &v1.Observation{InstanceId: result.InstanceID, InboundTag: result.InboundTag, Generation: result.Generation, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}
}
func failure(err error) error {
	code, message := codes.Unavailable, "unavailable"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code, message = codes.DeadlineExceeded, "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		code, message = codes.Canceled, "canceled"
	case errors.Is(err, domain.ErrStale):
		code, message = codes.Aborted, "stale_observation"
	case errors.Is(err, domain.ErrIncompatible):
		code, message = codes.FailedPrecondition, "incompatible_account"
	case errors.Is(err, domain.ErrInboundMissing):
		code, message = codes.NotFound, "inbound_not_found"
	case errors.Is(err, domain.ErrInboundAmbiguous):
		code, message = codes.FailedPrecondition, "ambiguous_inbound"
	case errors.Is(err, domain.ErrInvalidArgument):
		code, message = codes.InvalidArgument, "invalid_argument"
	case errors.Is(err, domain.ErrNotFound):
		code, message = codes.NotFound, "not_found"
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrNotRunning):
		code, message = codes.FailedPrecondition, "failed_precondition"
	case errors.Is(err, domain.ErrUncertain):
		code, message = codes.FailedPrecondition, "restart_required"
	case errors.Is(err, domain.ErrExecutionChanged):
		code, message = codes.Aborted, "execution_changed"
	case errors.Is(err, domain.ErrCapacity):
		code, message = codes.ResourceExhausted, "resource_exhausted"
	}
	return status.Error(code, message)
}
