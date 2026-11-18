package grpcapi

import (
	"context"
	"errors"

	cpuv1 "github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (handler Handler) GetHostCPUTimes(ctx context.Context, request *cpuv1.GetHostCPUTimesRequest) (*cpuv1.GetHostCPUTimesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.ReadTimes(ctx, request.GetPerCpu())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Error(codes.Unavailable, "host CPU times unavailable")
	}
	rows := make([]*cpuv1.CPUTicks, 0, len(result.Times.Counters))
	for _, row := range result.Times.Counters {
		rows = append(rows, &cpuv1.CPUTicks{Cpu: row.CPU, UserTicks: row.User, NiceTicks: row.Nice, SystemTicks: row.System, IdleTicks: row.Idle, IowaitTicks: row.IOWait, IrqTicks: row.IRQ, SoftirqTicks: row.SoftIRQ, StealTicks: row.Steal, GuestTicks: row.Guest, GuestNiceTicks: row.GuestNice})
	}
	return &cpuv1.GetHostCPUTimesResponse{PerCpu: result.Times.PerCPU, TicksPerSecond: result.Times.TicksPerSecond, Counters: rows, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
