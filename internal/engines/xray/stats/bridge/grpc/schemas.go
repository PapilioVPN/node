package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/runtime_stats.proto

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func response(result runtime.Snapshot, err error) (*statsv1.XrayRuntimeStats, error) {
	if err != nil {
		switch {
		case errors.Is(err, lifecycle.ErrNotFound):
			return nil, status.Error(codes.NotFound, lifecycle.ErrNotFound.Error())
		case errors.Is(err, runtime.ErrBusy):
			return nil, status.Error(codes.ResourceExhausted, runtime.ErrBusy.Error())
		case errors.Is(err, context.Canceled):
			return nil, status.Error(codes.Canceled, "xray statistics request canceled")
		case errors.Is(err, context.DeadlineExceeded):
			return nil, status.Error(codes.DeadlineExceeded, "xray statistics request deadline exceeded")
		default:
			return nil, status.Error(codes.Unavailable, runtime.ErrUnavailable.Error())
		}
	}
	return &statsv1.XrayRuntimeStats{
		Goroutines: result.Goroutines, GcCount: result.GCCount,
		HeapAllocatedBytes: result.HeapAllocatedBytes, TotalAllocatedBytes: result.TotalAllocatedBytes,
		SystemBytes: result.SystemBytes, Mallocs: result.Mallocs, Frees: result.Frees,
		LiveObjects: result.LiveObjects, GcPauseTotalNanoseconds: result.GCPauseTotalNanoseconds,
		UptimeSeconds: result.UptimeSeconds,
	}, nil
}
