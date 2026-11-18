// Package grpcapi exposes process observations through versioned gRPC contracts.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/processes/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/domain"
	processesv1 "github.com/PapilioVPN/node/internal/apps/system/host/processes/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	processesv1.UnimplementedHostProcessesServiceServer
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(server grpc.ServiceRegistrar) {
	processesv1.RegisterHostProcessesServiceServer(server, handler)
}

func (handler Handler) CountProcesses(ctx context.Context, _ *processesv1.CountProcessesRequest) (*processesv1.CountProcessesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.Count(ctx)
	if err != nil {
		return nil, readError(err)
	}
	return &processesv1.CountProcessesResponse{Count: result.Count, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func (handler Handler) GetProcess(ctx context.Context, input *processesv1.GetProcessRequest) (*processesv1.GetProcessResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.Read(ctx, input.GetPid())
	if err != nil {
		return nil, readError(err)
	}
	return &processesv1.GetProcessResponse{Process: processResult(result.Process), TicksPerSecond: result.TicksPerSecond, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func (handler Handler) ListProcesses(ctx context.Context, input *processesv1.ListProcessesRequest) (*processesv1.ListProcessesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.List(ctx, domain.Selection{AfterPID: input.GetAfterPid(), Limit: input.GetLimit()})
	if err != nil {
		return nil, readError(err)
	}
	rows := make([]*processesv1.ProcessEntry, 0, len(result.Page.Entries))
	for _, entry := range result.Page.Entries {
		row := &processesv1.ProcessEntry{Pid: entry.PID, Issue: entry.Issue}
		if entry.Process != nil {
			row.Process = processResult(*entry.Process)
		}
		rows = append(rows, row)
	}
	return &processesv1.ListProcessesResponse{Entries: rows, NextAfterPid: result.Page.NextAfterPID, TicksPerSecond: result.TicksPerSecond, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func processResult(row domain.Process) *processesv1.Process {
	return &processesv1.Process{Pid: row.PID, ParentPid: row.ParentPID, Name: row.Name, State: row.State, Threads: row.Threads, Priority: row.Priority, Nice: row.Nice, StartTicks: row.StartTicks, UserTicks: row.UserTicks, SystemTicks: row.SystemTicks, VirtualBytes: row.VirtualBytes, ResidentBytes: row.ResidentBytes}
}
func readError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	code, message := codes.Unavailable, "process observation unavailable"
	switch {
	case errors.Is(err, domain.ErrInvalidArgument):
		code, message = codes.InvalidArgument, "invalid process selection"
	case errors.Is(err, domain.ErrNotFound):
		code, message = codes.NotFound, "process no longer visible"
	case errors.Is(err, domain.ErrPermissionDenied):
		code, message = codes.PermissionDenied, "process access denied"
	case errors.Is(err, domain.ErrResourceExhausted):
		code, message = codes.ResourceExhausted, "process inventory exceeds supported bound"
	}
	return status.Error(code, message)
}
