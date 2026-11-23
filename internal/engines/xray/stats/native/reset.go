package native

import (
	"context"
	"errors"

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
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	key := string(counter.Scope) + ">>>" + counter.Name + ">>>traffic>>>" + string(counter.Direction)
	var reply NamedCounterResponse
	err = conn.Invoke(ctx, resetCounter, &ResetCounterRequest{Name: key, Reset_: true}, &reply, resetOptions...)
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
