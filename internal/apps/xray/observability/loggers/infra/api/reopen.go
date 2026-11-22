// Package api adapts the pinned native LoggerService without replaying mutations.
package api

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	logger "github.com/xtls/xray-core/app/log/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type Restarter struct{ client logger.LoggerServiceClient }

func New(connection grpc.ClientConnInterface) Restarter {
	return Restarter{client: logger.NewLoggerServiceClient(connection)}
}
func (r Restarter) Reopen(ctx context.Context) (domain.NativeResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.NativeResult{Effect: domain.NotApplied}, err
	}
	var remote peer.Peer
	_, err := r.client.RestartLogger(ctx, &logger.RestartLoggerRequest{}, grpc.Peer(&remote))
	if err == nil {
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}
	nativeStatus, terminal := status.FromError(err)
	if errors.Is(err, rpc.ErrClosed) || nativeStatus.Code() == codes.Unimplemented {
		return domain.NativeResult{Effect: domain.NotApplied}, errors.Join(domain.ErrUnsupported, err)
	}
	contextErr := ctx.Err()
	if contextErr == nil {
		switch nativeStatus.Code() {
		case codes.DeadlineExceeded:
			contextErr = context.DeadlineExceeded
		case codes.Canceled:
			contextErr = context.Canceled
		}
	}
	// In the pinned gRPC client, Peer is populated when a transport stream exists.
	// Without a stream, the unary request message was never submitted. In
	// particular, a refused initial connection must not poison the generation.
	if remote.Addr == nil {
		return domain.NativeResult{Effect: domain.NotApplied}, errors.Join(err, contextErr)
	}
	// In this pinned handler, native Close/Start errors return ordinary Go errors
	// as a terminal Unknown status. The phase is unknown, but that RPC has returned.
	// Cancellation/transport failure cannot establish that remote file I/O stopped.
	unresolved := contextErr != nil || !terminal || nativeStatus.Code() != codes.Unknown
	return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: unresolved}, errors.Join(err, contextErr)
}
