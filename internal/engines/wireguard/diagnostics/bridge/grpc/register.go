package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/wireguard_diagnostics.proto

import (
	"context"
	"errors"

	diagnosticsv1 "github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/events"
	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/probe"
	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/process"
	linkstatus "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type streamCancelKey struct{}

func TransportContext(ctx context.Context, info *tap.Info) (context.Context, error) {
	if info.FullMethodName != diagnosticsv1.WireGuardDiagnosticsService_GetLogs_FullMethodName {
		return ctx, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	return context.WithValue(ctx, streamCancelKey{}, cancel), nil
}

type handlers struct {
	diagnosticsv1.UnimplementedWireGuardDiagnosticsServiceServer
	logs    *events.Service
	runtime *process.Runtime
	probe   *probe.Service
}

func Register(server grpc.ServiceRegistrar, logs *events.Service, runtime *process.Runtime, check *probe.Service) {
	diagnosticsv1.RegisterWireGuardDiagnosticsServiceServer(server, &handlers{logs: logs, runtime: runtime, probe: check})
}

func (h *handlers) ProbeEgress(ctx context.Context, req *diagnosticsv1.WireGuardDiagnosticsRequest) (*diagnosticsv1.WireGuardEgressProbe, error) {
	result, err := h.probe.Run(ctx, req.GetName())
	if err != nil {
		return nil, rpcError(err)
	}
	return &diagnosticsv1.WireGuardEgressProbe{Reachable: result.Reachable, HttpStatus: uint32(result.HTTPStatus), ElapsedNanoseconds: result.ElapsedNanoseconds, ObservedAt: timestamppb.New(result.ObservedAt)}, nil
}

func (h *handlers) GetLogs(req *diagnosticsv1.WireGuardDiagnosticsRequest, stream grpc.ServerStreamingServer[diagnosticsv1.WireGuardLogEvent]) error {
	ctx := stream.Context()
	cancel, ok := ctx.Value(streamCancelKey{}).(context.CancelFunc)
	if !ok {
		return status.Error(codes.Internal, "log stream cancellation unavailable")
	}
	defer cancel()
	sub, err := h.logs.Subscribe(ctx, req.GetName())
	if err != nil {
		return rpcError(err)
	}
	defer sub.Close()
	stop := context.AfterFunc(sub.Context, cancel)
	defer stop()
	if err := stream.SendHeader(nil); err != nil {
		return err
	}
	for {
		event, err := sub.Next()
		if err != nil {
			return err
		}
		if err := stream.Send(&diagnosticsv1.WireGuardLogEvent{Sequence: event.Sequence, At: timestamppb.New(event.At), Kind: event.Kind}); err != nil {
			return err
		}
	}
}

func (h *handlers) GetRuntimeStats(ctx context.Context, req *diagnosticsv1.WireGuardDiagnosticsRequest) (*diagnosticsv1.WireGuardRuntimeStats, error) {
	stats, err := h.runtime.Read(ctx, req.GetName())
	if err != nil {
		return nil, rpcError(err)
	}
	return &diagnosticsv1.WireGuardRuntimeStats{Goroutines: uint64(stats.Goroutines), GcCount: stats.GCCount, HeapBytes: stats.HeapBytes,
		AllocatedBytes: stats.AllocatedBytes, SystemBytes: stats.SystemBytes, Allocations: stats.Allocations, Frees: stats.Frees,
		LiveObjects: stats.LiveObjects, GcPauseNanoseconds: stats.GCPauseNanoseconds, UptimeSeconds: stats.Uptime()}, nil
}

func rpcError(err error) error {
	switch {
	case errors.Is(err, linkstatus.ErrNotFound):
		return status.Error(codes.NotFound, linkstatus.ErrNotFound.Error())
	case errors.Is(err, events.ErrCapacity), errors.Is(err, linkstatus.ErrBusy):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	default:
		return status.Error(codes.Unavailable, "wireguard diagnostics unavailable")
	}
}
