// Package grpcapi exposes versioned live inbound operations.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	inboundsv1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	inboundsv1.UnimplementedInboundsServiceServer
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(registrar grpc.ServiceRegistrar) {
	inboundsv1.RegisterInboundsServiceServer(registrar, h)
}
func (h Handler) ListInbounds(ctx context.Context, request *inboundsv1.ListInboundsRequest) (*inboundsv1.InboundConfigurations, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Records(ctx, request.InstanceId)
	if err != nil {
		return nil, failure(err)
	}
	records := make([]*inboundsv1.InboundConfiguration, len(result.Inbounds))
	for i, item := range result.Inbounds {
		records[i] = &inboundsv1.InboundConfiguration{Tag: item.Tag, Configuration: item.Configuration}
	}
	return &inboundsv1.InboundConfigurations{InstanceId: result.InstanceID, Generation: result.Generation, Inbounds: records, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func (h Handler) ListInboundTags(ctx context.Context, request *inboundsv1.ListInboundTagsRequest) (*inboundsv1.InboundTags, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Tags(ctx, request.InstanceId)
	if err != nil {
		return nil, failure(err)
	}
	return &inboundsv1.InboundTags{InstanceId: result.InstanceID, Generation: result.Generation, Tags: result.Tags, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func (h Handler) GetInboundControlStatus(ctx context.Context, request *inboundsv1.GetInboundControlStatusRequest) (*inboundsv1.InboundControlStatus, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Status(ctx, request.InstanceId)
	if err != nil {
		return nil, failure(err)
	}
	response := &inboundsv1.InboundControlStatus{InstanceId: result.InstanceID, Generation: result.Generation, ExecutionState: result.ExecutionState, ApiConfigured: result.APIConfigured, RequestInFlight: result.RequestInFlight, RequiresInstanceRestart: result.RequiresInstanceRestart, ObservedAt: timestamppb.New(result.ObservedAt)}
	if result.LastOperation != nil {
		response.LastOperation = operationResult(*result.LastOperation)
	}
	return response, nil
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
