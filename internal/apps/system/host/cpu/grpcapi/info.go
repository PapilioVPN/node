package grpcapi

import (
	"context"
	"errors"

	cpuv1 "github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (handler Handler) GetHostCPUInfo(ctx context.Context, _ *cpuv1.GetHostCPUInfoRequest) (*cpuv1.GetHostCPUInfoResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.ReadInfo(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Error(codes.Unavailable, "host CPU information unavailable")
	}
	rows := make([]*cpuv1.ProcessorInfo, 0, len(result.Info.Processors))
	for _, row := range result.Info.Processors {
		rows = append(rows, &cpuv1.ProcessorInfo{Id: row.ID, VendorId: row.VendorID, Family: row.Family, Model: row.Model, ModelName: row.ModelName, Stepping: row.Stepping, Microcode: row.Microcode, PackageId: row.PackageID, CoreId: row.CoreID, ReportedCores: row.ReportedCores, ReportedSiblings: row.ReportedSiblings, ReportedMhz: row.ReportedMHz, CacheBytes: row.CacheBytes, ReportedFeatures: row.ReportedFeatures, ReportedBugs: row.ReportedBugs})
	}
	return &cpuv1.GetHostCPUInfoResponse{LogicalCount: result.Info.LogicalCount, PackageCount: result.Info.PackageCount, PhysicalCoreCount: result.Info.PhysicalCoreCount, Processors: rows, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
