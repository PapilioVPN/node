// Package grpcapi exposes versioned outbound observations.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	v1.UnimplementedOutboundsServiceServer
	Service      *services.Service
	Sources      *services.Sources
	Replacements *services.Replacements
	Timeout      time.Duration
}

func (h Handler) Register(registrar grpc.ServiceRegistrar) {
	v1.RegisterOutboundsServiceServer(registrar, h)
}
func (h Handler) ListOutbounds(ctx context.Context, request *v1.ListOutboundsRequest) (*v1.OutboundConfigurations, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.List(ctx, request.InstanceId, domain.PageRequest{PageSize: request.PageSize, PageToken: request.PageToken})
	if err != nil {
		return nil, failure(err)
	}
	records := make([]*v1.OutboundConfiguration, len(result.Outbounds))
	for i, item := range result.Outbounds {
		records[i] = &v1.OutboundConfiguration{Tag: item.Tag, Configuration: item.Configuration}
	}
	return &v1.OutboundConfigurations{InstanceId: result.InstanceID, Generation: result.Generation, Outbounds: records, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration), NextPageToken: result.NextPageToken, Source: result.Source, DefaultState: result.DefaultState}, nil
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
	case errors.Is(err, domain.ErrPlanExpired):
		code, message = codes.FailedPrecondition, "plan_expired"
	case errors.Is(err, domain.ErrStalePlan):
		code, message = codes.Aborted, "stale_plan"
	case errors.Is(err, domain.ErrContinuation):
		code, message = codes.FailedPrecondition, "continuation_expired"
	case errors.Is(err, domain.ErrOutboundNotFound):
		code, message = codes.NotFound, "outbound_not_found"
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

func (h Handler) GetOutboundControlStatus(ctx context.Context, request *v1.GetOutboundControlStatusRequest) (*v1.OutboundControlStatus, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Status(ctx, request.InstanceId)
	if err != nil {
		return nil, failure(err)
	}
	response := &v1.OutboundControlStatus{InstanceId: result.InstanceID, Generation: result.Generation, ExecutionState: result.ExecutionState, ApiConfigured: result.APIConfigured, RequestInFlight: result.RequestInFlight, RequiresInstanceRestart: result.RequiresInstanceRestart, ObservedAt: timestamppb.New(result.ObservedAt)}
	if result.LastOperation != nil {
		response.LastOperation = operationResult(*result.LastOperation)
	}
	return response, nil
}
