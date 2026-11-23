package native

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const queryStats = "/xray.app.stats.command.StatsService/QueryStats"

type trafficInstances interface {
	NativeConnection(id string) (*grpc.ClientConn, error)
}

type Traffic struct {
	instances trafficInstances
}

func NewTraffic(instances trafficInstances) *Traffic {
	return &Traffic{instances: instances}
}

func (client *Traffic) Query(ctx context.Context, id, pattern string, maxBytes int) (traffic.Counters, error) {
	conn, err := client.instances.NativeConnection(id)
	if err != nil {
		return traffic.Counters{}, err
	}
	var response QueryStatsResponse
	err = conn.Invoke(ctx, queryStats, &QueryStatsRequest{Pattern: pattern}, &response,
		grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(maxBytes))
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return traffic.Counters{}, cause
		}
		if status.Code(err) == codes.ResourceExhausted {
			return traffic.Counters{}, errors.Join(traffic.ErrTooLarge, err)
		}
		return traffic.Counters{}, err
	}
	current, err := client.instances.NativeConnection(id)
	if err != nil {
		return traffic.Counters{}, err
	}
	if current != conn {
		return traffic.Counters{}, traffic.ErrUnavailable
	}
	return traffic.Counters{
		Count: len(response.Stats),
		Each: func(yield func(string, int64) bool) {
			for _, counter := range response.Stats {
				if !yield(counter.GetName(), counter.GetValue()) {
					return
				}
			}
		},
	}, nil
}
