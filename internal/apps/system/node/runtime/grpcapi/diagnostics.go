package grpcapi

import (
	"context"
	"time"

	runtimev1 "github.com/PapilioVPN/node/internal/apps/system/node/runtime/grpcapi/proto/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (handler Handler) GetNodeDiagnostics(ctx context.Context, _ *runtimev1.GetNodeDiagnosticsRequest) (*runtimev1.GetNodeDiagnosticsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.Diagnostics(ctx)
	if err != nil {
		return nil, readError(err)
	}
	response := &runtimev1.GetNodeDiagnosticsResponse{Healthy: result.Healthy, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}
	if result.Diagnostics.HTTP != nil {
		response.Http = &runtimev1.ListenerStatus{Ready: result.Diagnostics.HTTP.Ready}
	}
	if result.Diagnostics.GRPC != nil {
		response.Grpc = &runtimev1.ListenerStatus{Ready: result.Diagnostics.GRPC.Ready}
	}
	if row := result.Diagnostics.NATS; row != nil {
		broker := &runtimev1.BrokerStatus{State: row.State, ProbeSucceeded: row.ProbeSucceeded, InMessages: row.InMessages, OutMessages: row.OutMessages, InBytes: row.InBytes, OutBytes: row.OutBytes, Reconnects: row.Reconnects}
		if row.ProbeSucceeded {
			duration := int64(row.RoundTrip / time.Nanosecond)
			broker.RoundTripNs = &duration
		}
		if row.ErrorCode != "" {
			code := row.ErrorCode
			broker.ErrorCode = &code
		}
		response.Nats = broker
	}
	return response, nil
}
