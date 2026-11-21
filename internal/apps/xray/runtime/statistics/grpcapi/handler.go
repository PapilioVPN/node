// Package grpcapi adapts Xray runtime facts to its versioned management service.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/domain"
	statisticsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	statisticsv1.UnimplementedStatisticsServiceServer
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(registrar grpc.ServiceRegistrar) {
	statisticsv1.RegisterStatisticsServiceServer(registrar, h)
}
func (h Handler) GetRuntimeStatistics(ctx context.Context, request *statisticsv1.GetRuntimeStatisticsRequest) (*statisticsv1.RuntimeStatistics, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Read(ctx, request.GetInstanceId())
	if err != nil {
		return nil, failure(err)
	}
	s := result.Statistics
	return &statisticsv1.RuntimeStatistics{InstanceId: result.InstanceID, Generation: result.Generation, Goroutines: s.Goroutines, GcCycles: s.GCCycles, HeapAllocatedBytes: s.HeapAllocatedBytes, HeapTotalAllocatedBytes: s.HeapTotalAllocatedBytes, RuntimeSysBytes: s.RuntimeSysBytes, Allocations: s.Allocations, Frees: s.Frees, LiveObjects: s.LiveObjects, GcPauseTotalNs: s.GCPauseTotalNS, ApiUptimeSeconds: s.APIUptimeSeconds, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
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
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrClosed), errors.Is(err, domain.ErrNotRunning):
		code, message = codes.FailedPrecondition, "failed_precondition"
	case errors.Is(err, domain.ErrExecutionChanged):
		code, message = codes.Aborted, "execution_changed"
	case errors.Is(err, domain.ErrCapacity):
		code, message = codes.ResourceExhausted, "resource_exhausted"
	}
	return status.Error(code, message)
}
