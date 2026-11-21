// Package grpcapi adapts candidate checks to versioned native gRPC.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	configsv1.UnimplementedConfigurationsServiceServer
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(registrar grpc.ServiceRegistrar) {
	configsv1.RegisterConfigurationsServiceServer(registrar, h)
}

func (h Handler) ValidateConfiguration(ctx context.Context, request *configsv1.ValidateConfigurationRequest) (*configsv1.ConfigurationValidation, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid_argument")
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Validate(ctx, request.InstanceId, domain.Candidate{Format: domain.Format(request.Format), Content: request.Content})
	if err != nil {
		return nil, failure(err)
	}
	return &configsv1.ConfigurationValidation{InstanceId: result.InstanceID, Valid: result.Valid, CheckedAt: timestamppb.New(result.CheckedAt), DurationNs: int64(result.Duration)}, nil
}

func failure(err error) error {
	code, message := codes.Unavailable, "unavailable"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code, message = codes.DeadlineExceeded, "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		code, message = codes.Canceled, "canceled"
	case errors.Is(err, domain.ErrInvalidArgument):
		code, message = codes.InvalidArgument, "invalid_argument"
	case errors.Is(err, domain.ErrRejected):
		code, message = codes.InvalidArgument, "native_rejected"
	case errors.Is(err, domain.ErrNotFound):
		code, message = codes.NotFound, "not_found"
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrClosed):
		code, message = codes.FailedPrecondition, "failed_precondition"
	case errors.Is(err, domain.ErrTooLarge), errors.Is(err, domain.ErrCapacity):
		code, message = codes.ResourceExhausted, "resource_exhausted"
	}
	return status.Error(code, message)
}
