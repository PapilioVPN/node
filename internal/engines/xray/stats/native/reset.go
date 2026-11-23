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

const resetCounter = "/xray.app.stats.command.StatsService/GetStats"

var resetOptions = []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(8 << 10)}

func (client *Traffic) Reset(ctx context.Context, counter traffic.Counter) (int64, error) {
	conn, err := client.instances.NativeConnection(counter.ID)
	if err != nil {
		return 0, err
	}
	prefix := string(counter.Scope) + ">>>" + counter.Name + ">>>traffic>>>"
	return swapCounter(ctx, conn, prefix+string(counter.Direction))
}

func (client *Traffic) ResetPair(ctx context.Context, pair traffic.CounterPair) (traffic.PairReset, error) {
	conn, err := client.instances.NativeConnection(pair.ID)
	if err != nil {
		return traffic.PairReset{}, err
	}
	if err := ctx.Err(); err != nil {
		return traffic.PairReset{}, err
	}
	if client.scopeBatch {
		result, err := resetNativePair(ctx, conn, pair)
		if status.Code(err) != codes.Unimplemented {
			return result, err
		}
		if err := client.checkConnection(pair.ID, conn); err != nil {
			return traffic.PairReset{}, err
		}
	}
	prefix := string(pair.Scope) + ">>>" + pair.Name + ">>>traffic>>>"
	result := traffic.PairReset{Downlink: traffic.DirectionReset{Effect: traffic.ResetNotAttempted}}
	result.Uplink, err = resetDirection(ctx, conn, prefix+"uplink")
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := client.checkConnection(pair.ID, conn); err != nil {
		return result, err
	}
	result.Downlink, err = resetDirection(ctx, conn, prefix+"downlink")
	return result, err
}

func resetNativePair(ctx context.Context, conn *grpc.ClientConn, pair traffic.CounterPair) (traffic.PairReset, error) {
	result, err := scopeapi.NewExactTrafficClient(conn).ResetPair(ctx, &scopeapi.PairResetRequest{
		Scope: string(pair.Scope), Name: pair.Name,
	}, grpc.MaxCallRecvMsgSize(1024))
	if status.Code(err) == codes.Unimplemented {
		// An older companion never dispatched the reset. Use the stock path.
		return traffic.PairReset{}, err
	}
	if err != nil {
		unknown := traffic.DirectionReset{Effect: traffic.ResetUnknown}
		return traffic.PairReset{Uplink: unknown, Downlink: unknown}, errors.Join(traffic.ErrResetUnknown, err, ctx.Err())
	}
	return traffic.PairReset{Uplink: confirmedDirection(result.PreviousUplink), Downlink: confirmedDirection(result.PreviousDownlink)}, nil
}

func confirmedDirection(previous *int64) traffic.DirectionReset {
	if previous == nil {
		return traffic.DirectionReset{Effect: traffic.ResetMissing}
	}
	return traffic.DirectionReset{Effect: traffic.ResetAcknowledged, PreviousBytes: *previous}
}

func resetDirection(ctx context.Context, conn *grpc.ClientConn, key string) (traffic.DirectionReset, error) {
	value, err := swapCounter(ctx, conn, key)
	switch {
	case err == nil:
		return traffic.DirectionReset{Effect: traffic.ResetAcknowledged, PreviousBytes: value}, nil
	case errors.Is(err, traffic.ErrResetUnknown):
		return traffic.DirectionReset{Effect: traffic.ResetUnknown}, err
	case errors.Is(err, traffic.ErrNotFound):
		return traffic.DirectionReset{Effect: traffic.ResetMissing}, nil
	default:
		return traffic.DirectionReset{Effect: traffic.ResetNotApplied}, err
	}
}

func swapCounter(ctx context.Context, conn *grpc.ClientConn, key string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var reply NamedCounterResponse
	err := conn.Invoke(ctx, resetCounter, &ResetCounterRequest{Name: key, Reset_: true}, &reply, resetOptions...)
	switch status.Code(err) {
	case codes.OK:
		if reply.GetStat().GetName() != key {
			return 0, traffic.ErrResetUnknown
		}
		// The response acknowledges this execution's reset even if it has since stopped.
		return reply.Stat.Value, nil
	case codes.NotFound:
		return 0, errors.Join(traffic.ErrNotFound, err)
	case codes.Unimplemented:
		return 0, errors.Join(traffic.ErrUnavailable, err)
	default:
		return 0, errors.Join(traffic.ErrResetUnknown, err, ctx.Err())
	}
}
