package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	cpuv1 "github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (handler Handler) GetHostCPUUsage(ctx context.Context, request *cpuv1.GetHostCPUUsageRequest) (*cpuv1.GetHostCPUUsageResponse, error) {
	input := domain.UsageRequest{PerCPU: request.GetPerCpu(), Interval: domain.DefaultSampleInterval}
	if request != nil && request.IntervalNs != nil {
		input.Interval = time.Duration(*request.IntervalNs)
	}
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.SampleUsage(ctx, input)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidSampleInterval) {
			return nil, status.Error(codes.InvalidArgument, "invalid CPU sample interval")
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Error(codes.Unavailable, "host CPU usage unavailable")
	}
	rows := make([]*cpuv1.CPUUsage, 0, len(result.CPUs))
	for _, row := range result.CPUs {
		rows = append(rows, &cpuv1.CPUUsage{Cpu: row.CPU, State: usageState(row.State), Percent: row.Percent, BusyTicks: row.BusyTicks, TotalTicks: row.TotalTicks})
	}
	return &cpuv1.GetHostCPUUsageResponse{PerCpu: result.PerCPU, TicksPerSecond: result.TicksPerSecond, Cpus: rows, RequestedIntervalNs: int64(result.RequestedInterval), StartedAt: timestamppb.New(result.StartedAt), SampleIntervalNs: int64(result.SampleInterval), ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}

func usageState(state domain.UsageState) cpuv1.CPUUsageState {
	switch state {
	case domain.UsageMeasured:
		return cpuv1.CPUUsageState_CPU_USAGE_STATE_MEASURED
	case domain.UsageNoProgress:
		return cpuv1.CPUUsageState_CPU_USAGE_STATE_NO_PROGRESS
	case domain.UsageCounterDecreased:
		return cpuv1.CPUUsageState_CPU_USAGE_STATE_COUNTER_DECREASED
	case domain.UsageCounterOverflow:
		return cpuv1.CPUUsageState_CPU_USAGE_STATE_COUNTER_OVERFLOW
	case domain.UsageAdded:
		return cpuv1.CPUUsageState_CPU_USAGE_STATE_ADDED
	case domain.UsageRemoved:
		return cpuv1.CPUUsageState_CPU_USAGE_STATE_REMOVED
	default:
		return cpuv1.CPUUsageState_CPU_USAGE_STATE_UNSPECIFIED
	}
}
