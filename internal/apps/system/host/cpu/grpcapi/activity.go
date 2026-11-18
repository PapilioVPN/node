package grpcapi

import (
	"context"
	"errors"

	cpuv1 "github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (handler Handler) GetHostCPUActivity(ctx context.Context, _ *cpuv1.GetHostCPUActivityRequest) (*cpuv1.GetHostCPUActivityResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.ReadActivity(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Error(codes.Unavailable, "host CPU activity unavailable")
	}
	return &cpuv1.GetHostCPUActivityResponse{
		ContextSwitches: result.Activity.ContextSwitches,
		Interrupts:      result.Activity.Interrupts,
		SoftInterrupts:  result.Activity.SoftInterrupts,
		CreatedTasks:    result.Activity.CreatedTasks,
		RunnableTasks:   result.Activity.RunnableTasks,
		IoBlockedTasks:  result.Activity.IOBlockedTasks,
		ObservedAt:      timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration),
	}, nil
}
