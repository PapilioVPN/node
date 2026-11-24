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
		Scope: string(request.Scope), MaxCounters: request.MaxCounters, CompactReply: true,
	}, grpc.MaxCallRecvMsgSize(1<<20))
	if status.Code(err) == codes.Unimplemented {
		return nil, errors.Join(traffic.ErrUnavailable, err)
	}
	if err != nil {
		return nil, errors.Join(traffic.ErrResetUnknown, err, ctx.Err())
	}
	rows := result.GetCounters()
	compact := result.GetCompactCounters()
	if result.GetLimitExceeded() {
		if len(rows) != 0 || compact != nil {
			return nil, traffic.ErrResetUnknown
		}
		return nil, traffic.ErrTooLarge
	}
	count := len(rows)
	if compact != nil {
		if count != 0 || len(compact.Names) != len(compact.Downlink) || len(compact.Names) != len(compact.PreviousBytes) {
			return nil, traffic.ErrResetUnknown
		}
		count = len(compact.Names)
	}
	if count > int(request.MaxCounters) {
		return nil, traffic.ErrResetUnknown
	}
	report := make([]traffic.CounterReset, count)
	var nameBytes int
	for i := range report {
		var name string
		var downlink bool
		var previous int64
		if compact != nil {
			name, downlink, previous = compact.Names[i], compact.Downlink[i], compact.PreviousBytes[i]
		} else {
			row := rows[i]
			name, downlink, previous = row.GetName(), row.GetDownlink(), row.GetPreviousBytes()
		}
		nameBytes += len(name)
		if len(name) == 0 || len(name) > traffic.MaxCounterNameBytes || nameBytes > traffic.MaxScopeResetNameBytes {
			return nil, traffic.ErrResetUnknown
		}
		direction := traffic.Uplink
		if downlink {
			direction = traffic.Downlink
		}
		report[i] = traffic.CounterReset{
			Name: name, Direction: direction,
			DirectionReset: traffic.DirectionReset{Effect: traffic.ResetAcknowledged, PreviousBytes: previous},
		}
	}
	if err := orderScopeReport(report); err != nil {
		return nil, errors.Join(traffic.ErrResetUnknown, err)
	}
	return report, nil
}
