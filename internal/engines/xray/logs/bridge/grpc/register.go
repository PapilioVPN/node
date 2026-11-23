package grpc

import (
	"context"
	"errors"
	"io"

	logsv1 "github.com/PapilioVPN/node/internal/engines/xray/logs/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/logs/live"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"
)

type cancelStreamKey struct{}

// TransportContext installs a cancellable context before gRPC binds the
// stream's write quota to it. An interceptor's context would leave a Send
// blocked by HTTP/2 flow control when the execution ends.
func TransportContext(ctx context.Context, info *tap.Info) (context.Context, error) {
	if info.FullMethodName != logsv1.XrayLogsService_GetLogs_FullMethodName {
		return ctx, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	return context.WithValue(ctx, cancelStreamKey{}, cancel), nil
}

type handlers struct {
	logsv1.UnimplementedXrayLogsServiceServer
	service *live.Service
}

func Register(server grpc.ServiceRegistrar, service *live.Service) {
	logsv1.RegisterXrayLogsServiceServer(server, &handlers{service: service})
}

func (h *handlers) GetLogs(req *logsv1.XrayLogsRequest, stream grpc.ServerStreamingServer[logsv1.XrayLogEvent]) error {
	ctx := stream.Context()
	cancel, ok := ctx.Value(cancelStreamKey{}).(context.CancelFunc)
	if !ok {
		return status.Error(codes.Internal, "xray log stream cancellation is unavailable")
	}
	defer cancel()
	sub, err := h.service.Subscribe(req.GetId())
	if err != nil {
		switch {
		case errors.Is(err, live.ErrNotFound):
			return status.Error(codes.NotFound, err.Error())
		case errors.Is(err, live.ErrInactive):
			return status.Error(codes.FailedPrecondition, err.Error())
		default:
			return status.Error(codes.ResourceExhausted, err.Error())
		}
	}
	defer sub.Close()
	execution := sub.ExecutionContext()
	stop := context.AfterFunc(execution, cancel)
	defer stop()

	if err := stream.SendHeader(nil); err != nil {
		return err
	}
	for {
		event, err := sub.Next(ctx)
		if err == nil {
			err = stream.Send(toMessage(event))
		}
		if execution.Err() != nil || errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func toMessage(event live.Event) *logsv1.XrayLogEvent {
	source := logsv1.XrayLogSource_XRAY_LOG_SOURCE_STDOUT
	if event.Source == live.Stderr {
		source = logsv1.XrayLogSource_XRAY_LOG_SOURCE_STDERR
	}
	return &logsv1.XrayLogEvent{
		Execution: event.Execution, Sequence: event.Sequence, Source: source,
		ReceivedUnixNano: event.ReceivedUnixNano, Message: []byte(event.Message),
		Truncated: event.Truncated,
	}
}
