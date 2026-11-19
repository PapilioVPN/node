// Package grpcapi exposes Node runtime information over gRPC.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/app/services"
	runtimev1 "github.com/PapilioVPN/node/internal/apps/system/node/runtime/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	runtimev1.UnimplementedNodeRuntimeServiceServer
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(server grpc.ServiceRegistrar) {
	runtimev1.RegisterNodeRuntimeServiceServer(server, handler)
}
func (handler Handler) GetNodeInfo(ctx context.Context, _ *runtimev1.GetNodeInfoRequest) (*runtimev1.GetNodeInfoResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.Info(ctx)
	if err != nil {
		return nil, readError(err)
	}
	row := result.Info
	return &runtimev1.GetNodeInfoResponse{GoVersion: row.GoVersion, Compiler: row.Compiler, OperatingSystem: row.OperatingSystem, Architecture: row.Architecture, Executable: row.Executable, Pid: row.PID, ParentPid: row.ParentPID, InitializedAt: timestamppb.New(row.InitializedAt), ModulePath: row.ModulePath, ModuleVersion: row.ModuleVersion, Revision: row.Revision, RevisionTime: row.RevisionTime, Modified: row.Modified, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func (handler Handler) GetNodeResources(ctx context.Context, _ *runtimev1.GetNodeResourcesRequest) (*runtimev1.GetNodeResourcesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.Resources(ctx)
	if err != nil {
		return nil, readError(err)
	}
	row := result.Resources
	return &runtimev1.GetNodeResourcesResponse{Pid: row.PID, StartTicks: row.StartTicks, TicksPerSecond: row.TicksPerSecond, UserTicks: row.UserTicks, SystemTicks: row.SystemTicks, VirtualBytes: row.VirtualBytes, ResidentBytes: row.ResidentBytes, Threads: row.Threads, FileDescriptors: row.FileDescriptors, Goroutines: row.Goroutines, GoMaxProcs: row.GoMaxProcs, RuntimeMappedBytes: row.RuntimeMappedBytes, HeapObjectsBytes: row.HeapObjectsBytes, HeapReleasedBytes: row.HeapReleasedBytes, AllocatedBytes: row.AllocatedBytes, FreedBytes: row.FreedBytes, GcCycles: row.GCCycles, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func readError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	return status.Error(codes.Unavailable, "Node runtime observation unavailable")
}
