// Package grpcapi adapts instance lifecycle operations to their versioned service.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/app/services"
	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/core/engines/instances/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Handler struct {
	instancesv1.UnimplementedInstancesServiceServer
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(server grpc.ServiceRegistrar) {
	instancesv1.RegisterInstancesServiceServer(server, h)
}
func (h Handler) GetInstanceStatus(ctx context.Context, request *instancesv1.GetInstanceStatusRequest) (*instancesv1.InstanceStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Status(ctx, request.GetInstanceId())
	if err != nil {
		return nil, operationError(err)
	}
	return statusResult(result), nil
}
func (h Handler) StartInstance(ctx context.Context, request *instancesv1.StartInstanceRequest) (*instancesv1.ControlResult, error) {
	return h.control(ctx, request.GetInstanceId(), h.Service.Start)
}
func (h Handler) StopInstance(ctx context.Context, request *instancesv1.StopInstanceRequest) (*instancesv1.ControlResult, error) {
	return h.control(ctx, request.GetInstanceId(), h.Service.Stop)
}
func (h Handler) RestartInstance(ctx context.Context, request *instancesv1.RestartInstanceRequest) (*instancesv1.ControlResult, error) {
	return h.control(ctx, request.GetInstanceId(), h.Service.Restart)
}
func (h Handler) control(ctx context.Context, id string, operation func(context.Context, string) (domain.Control, error)) (*instancesv1.ControlResult, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := operation(ctx, id)
	response := controlResult(result)
	if err != nil {
		failure := status.Convert(operationError(err))
		detailed, detailErr := failure.WithDetails(response)
		if detailErr != nil {
			return nil, failure.Err()
		}
		return nil, detailed.Err()
	}
	return response, nil
}
func (h Handler) CheckInstanceReadiness(ctx context.Context, request *instancesv1.CheckInstanceReadinessRequest) (*instancesv1.InstanceReadiness, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Check(ctx, request.GetInstanceId())
	if err != nil {
		return nil, operationError(err)
	}
	return readinessResult(result), nil
}
func operationError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	code := codes.Unavailable
	switch {
	case errors.Is(err, domain.ErrInvalidArgument):
		code = codes.InvalidArgument
	case errors.Is(err, domain.ErrNotFound):
		code = codes.NotFound
	case errors.Is(err, domain.ErrBusy):
		code = codes.ResourceExhausted
	case errors.Is(err, domain.ErrClosed), errors.Is(err, domain.ErrUnsupported):
		code = codes.FailedPrecondition
	}
	return status.Error(code, "instance operation failed")
}
