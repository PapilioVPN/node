package native

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/native/scopeapi"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func resetScopeBatch(ctx context.Context, conn *grpc.ClientConn, request traffic.ScopeReset) ([]traffic.CounterReset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := scopeapi.NewExactTrafficClient(conn).ResetScope(ctx, &scopeapi.ScopeResetRequest{
		Scope: string(request.Scope), MaxCounters: request.MaxCounters,
	}, grpc.MaxCallRecvMsgSize(1<<20))
	if status.Code(err) == codes.Unimplemented {
		return nil, errors.Join(traffic.ErrUnavailable, err)
	}
	if err != nil {
		return nil, errors.Join(traffic.ErrResetUnknown, err, ctx.Err())
	}
	rows := result.GetCounters()
	if result.GetLimitExceeded() {
		if len(rows) != 0 {
			return nil, traffic.ErrResetUnknown
		}
		return nil, traffic.ErrTooLarge
	}
	if len(rows) > int(request.MaxCounters) {
		return nil, traffic.ErrResetUnknown
	}
	report := make([]traffic.CounterReset, len(rows))
	var nameBytes int
	for i, row := range rows {
		name := row.GetName()
		nameBytes += len(name)
		if len(name) == 0 || len(name) > traffic.MaxCounterNameBytes || nameBytes > traffic.MaxScopeResetNameBytes {
			return nil, traffic.ErrResetUnknown
		}
		direction := traffic.Uplink
		if row.GetDownlink() {
			direction = traffic.Downlink
		}
		report[i] = traffic.CounterReset{
			Name: name, Direction: direction,
			DirectionReset: traffic.DirectionReset{Effect: traffic.ResetAcknowledged, PreviousBytes: row.GetPreviousBytes()},
		}
	}
	if err := orderScopeReport(report); err != nil {
		return nil, errors.Join(traffic.ErrResetUnknown, err)
	}
	return report, nil
}
