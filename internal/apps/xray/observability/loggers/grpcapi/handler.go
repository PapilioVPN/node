// Package grpcapi exposes logger operations and their versioned control evidence.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/domain"
	loggersv1 "github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	loggersv1.UnimplementedLoggersServiceServer
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(registrar grpc.ServiceRegistrar) {
	loggersv1.RegisterLoggersServiceServer(registrar, h)
}
func (h Handler) ReopenLogger(ctx context.Context, request *loggersv1.ReopenLoggerRequest) (*loggersv1.LoggerOperation, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Reopen(ctx, request.InstanceId)
	response := operationResult(result)
	if err != nil {
		detailed, detailErr := status.Convert(failure(err)).WithDetails(response)
		if detailErr != nil {
			return nil, failure(err)
		}
		return nil, detailed.Err()
	}
	return response, nil
}
func (h Handler) GetLoggerControlStatus(ctx context.Context, request *loggersv1.GetLoggerControlStatusRequest) (*loggersv1.LoggerControlStatus, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Status(ctx, request.InstanceId)
	if err != nil {
		return nil, failure(err)
	}
	response := &loggersv1.LoggerControlStatus{InstanceId: result.InstanceID, Generation: result.Generation, ExecutionState: string(result.ExecutionState), ApiConfigured: result.APIConfigured, RequestInFlight: result.RequestInFlight, RequiresInstanceRestart: result.RequiresInstanceRestart, ObservedAt: timestamppb.New(result.ObservedAt)}
	if result.LastOperation != nil {
		response.LastOperation = operationResult(*result.LastOperation)
	}
	return response, nil
}
func operationResult(result domain.Operation) *loggersv1.LoggerOperation {
	response := &loggersv1.LoggerOperation{InstanceId: result.InstanceID, Generation: result.Generation, Effect: string(result.Effect), Acknowledged: result.Acknowledged, RequiresInstanceRestart: result.RequiresInstanceRestart, DurationNs: int64(result.Duration)}
	if !result.ObservedAt.IsZero() {
		response.ObservedAt = timestamppb.New(result.ObservedAt)
	}
	return response
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
