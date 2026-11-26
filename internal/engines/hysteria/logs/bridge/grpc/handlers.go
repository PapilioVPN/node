// Package grpc exposes live redacted Hysteria events over gRPC.
package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/hysteria_logs.proto

import (
	"context"
	"errors"
	"io"

	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/grpcapi"
	v1 "github.com/PapilioVPN/node/internal/engines/hysteria/logs/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/hysteria/logs/live"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"
)

type cancelStreamKey struct{}

// TransportContext binds execution termination to gRPC's write quota. A
// stream interceptor context cannot unblock a Send stalled in flow control.
func TransportContext(ctx context.Context, info *tap.Info) (context.Context, error) {
	if info.FullMethodName != v1.HysteriaLogsService_GetLogs_FullMethodName {
		return ctx, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	return context.WithValue(ctx, cancelStreamKey{}, cancel), nil
}

type handler struct {
	v1.UnimplementedHysteriaLogsServiceServer
	service *live.Service
}

func Register(registrar grpc.ServiceRegistrar, service *live.Service) {
	v1.RegisterHysteriaLogsServiceServer(registrar, &handler{service: service})
}

func (h *handler) GetLogs(r *v1.HysteriaLogsRequest, stream grpc.ServerStreamingServer[v1.HysteriaLogEvent]) error {
	ctx := stream.Context()
	cancel, ok := ctx.Value(cancelStreamKey{}).(context.CancelFunc)
	if !ok {
		return status.Error(codes.Internal, "Hysteria log stream cancellation is unavailable")
	}
	defer cancel()
	sub, err := h.service.Subscribe(ctx, r.GetId())
	if err != nil {
		return grpcapi.ErrorStatus(err).Err()
	}
	defer sub.Close()
	lifetime := sub.Context()
	stop := context.AfterFunc(lifetime, cancel)
	defer stop()
	if err := stream.SendHeader(nil); err != nil {
		return err
	}
	for {
		event, err := sub.Next(ctx)
		if err == nil {
			err = stream.Send(&v1.HysteriaLogEvent{Execution: event.Execution, Sequence: event.Sequence, ReceivedUnixNano: event.ReceivedUnixNano, Event: event.Name})
		}
		if lifetime.Err() != nil || errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
