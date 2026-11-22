// Package grpcapi adapts exact traffic observations to their versioned service.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	trafficv1 "github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	trafficv1.UnimplementedTrafficServiceServer
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(registrar grpc.ServiceRegistrar) {
	trafficv1.RegisterTrafficServiceServer(registrar, h)
}
func (h Handler) GetTrafficCounter(ctx context.Context, request *trafficv1.GetTrafficCounterRequest) (*trafficv1.TrafficCounter, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	selection := domain.Selection{Name: request.Name}
	switch request.Scope {
	case trafficv1.Scope_SCOPE_USER:
		selection.Scope = domain.User
	case trafficv1.Scope_SCOPE_INBOUND:
		selection.Scope = domain.Inbound
	case trafficv1.Scope_SCOPE_OUTBOUND:
		selection.Scope = domain.Outbound
	default:
		return nil, failure(domain.ErrInvalidArgument)
	}
	switch request.Direction {
	case trafficv1.Direction_DIRECTION_UPLINK:
		selection.Direction = domain.Uplink
	case trafficv1.Direction_DIRECTION_DOWNLINK:
		selection.Direction = domain.Downlink
	default:
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Read(ctx, request.InstanceId, selection)
	if err != nil {
		return nil, failure(err)
	}
	return &trafficv1.TrafficCounter{InstanceId: result.InstanceID, Generation: result.Generation, Scope: request.Scope, Name: result.Selection.Name, Direction: request.Direction, Bytes: result.Bytes, Source: result.Source, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
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
	case errors.Is(err, domain.ErrConflict):
		code, message = codes.FailedPrecondition, "revision_conflict"
	case errors.Is(err, domain.ErrContinuation):
		code, message = codes.FailedPrecondition, "continuation_expired"
	case errors.Is(err, domain.ErrRestartRequired):
		code, message = codes.FailedPrecondition, "restart_required"
	case errors.Is(err, domain.ErrExecutionChanged):
		code, message = codes.Aborted, "execution_changed"
	case errors.Is(err, domain.ErrCounterNotFound):
		code, message = codes.NotFound, "counter_not_found"
	case errors.Is(err, domain.ErrNotFound):
		code, message = codes.NotFound, "not_found"
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrClosed), errors.Is(err, domain.ErrNotRunning):
		code, message = codes.FailedPrecondition, "failed_precondition"
	case errors.Is(err, domain.ErrCapacity):
		code, message = codes.ResourceExhausted, "resource_exhausted"
	}
	return status.Error(code, message)
}
