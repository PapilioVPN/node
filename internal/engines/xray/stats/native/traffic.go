package native

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	queryStats = "/xray.app.stats.command.StatsService/QueryStats"
	uplink     = ">>>traffic>>>uplink"
	downlink   = ">>>traffic>>>downlink"
)

var (
	namedTrafficOptions = []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(256 << 10)}
	listTrafficOptions  = []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(4 << 20)}
)

type activeInstances interface {
	NativeConnection(id string) (*grpc.ClientConn, error)
}

type Traffic struct {
	instances activeInstances
	lists     trafficLists
}

func NewTraffic(instances activeInstances) *Traffic {
	return &Traffic{instances: instances}
}

func (client *Traffic) Read(ctx context.Context, id string, scope traffic.Scope, name string) (traffic.Usage, error) {
	pattern := string(scope) + ">>>" + name + ">>>traffic>>>"
	counters, err := client.query(ctx, id, pattern, namedTrafficOptions)
	if err != nil {
		return traffic.Usage{}, err
	}

	entry := traffic.Usage{Name: name}
	for _, counter := range counters {
		direction, matches := strings.CutPrefix(counter.GetName(), pattern)
		if !matches {
			continue
		}
		switch direction {
		case "uplink":
			entry.UplinkBytes, entry.HasUplink = counter.GetValue(), true
		case "downlink":
			entry.DownlinkBytes, entry.HasDownlink = counter.GetValue(), true
		}
	}
	if !entry.HasUplink && !entry.HasDownlink {
		return traffic.Usage{}, traffic.ErrNotFound
	}
	return entry, nil
}

func (client *Traffic) List(ctx context.Context, id string, scope traffic.Scope) ([]traffic.Usage, error) {
	conn, err := client.instances.NativeConnection(id)
	if err != nil {
		return nil, err
	}
	prefix := string(scope) + ">>>"
	entries, err := client.lists.read(ctx, trafficListKey{conn: conn, scope: scope}, func(ctx context.Context) ([]traffic.Usage, error) {
		counters, err := readTrafficCounters(ctx, conn, prefix, listTrafficOptions)
		if err != nil {
			return nil, err
		}
		return groupTraffic(prefix, counters), nil
	})
	if err != nil {
		return nil, err
	}
	if err := client.checkConnection(id, conn); err != nil {
		return nil, err
	}
	return entries, nil
}

// The native statistics reads run without consulting their server context. The
// deadline still bounds the local wait and cancels the native gRPC stream.
type statsReadContext struct{ context.Context }

func (statsReadContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (client *Traffic) query(ctx context.Context, id, pattern string, opts []grpc.CallOption) ([]*TrafficCounter, error) {
	conn, err := client.instances.NativeConnection(id)
	if err != nil {
		return nil, err
	}
	counters, err := readTrafficCounters(ctx, conn, pattern, opts)
	if err != nil {
		return nil, err
	}
	if err := client.checkConnection(id, conn); err != nil {
		return nil, err
	}
	return counters, nil
}

func (client *Traffic) checkConnection(id string, conn *grpc.ClientConn) error {
	current, err := client.instances.NativeConnection(id)
	if err != nil {
		return err
	}
	if current != conn {
		return traffic.ErrUnavailable
	}
	return nil
}

func readTrafficCounters(ctx context.Context, conn *grpc.ClientConn, pattern string, opts []grpc.CallOption) ([]*TrafficCounter, error) {
	var reply QueryStatsResponse
	if err := conn.Invoke(statsReadContext{ctx}, queryStats, &QueryStatsRequest{Pattern: pattern}, &reply, opts...); err != nil {
		if cause := ctx.Err(); cause != nil {
			return nil, cause
		}
		if status.Code(err) == codes.ResourceExhausted {
			return nil, errors.Join(traffic.ErrTooLarge, err)
		}
		return nil, err
	}
	return reply.GetStats(), nil
}

func groupTraffic(prefix string, counters []*TrafficCounter) []traffic.Usage {
	grouped := make([]traffic.Usage, 0, len(counters)/2)
	positions := make(map[string]int, cap(grouped))
	for _, counter := range counters {
		key, inScope := strings.CutPrefix(counter.GetName(), prefix)
		if !inScope {
			continue
		}
		name, direction := splitTrafficCounter(key)
		if name == "" {
			continue
		}

		index, exists := positions[name]
		if !exists {
			index = len(grouped)
			positions[name] = index
			grouped = append(grouped, traffic.Usage{Name: name})
		}
		entry := &grouped[index]
		if direction == traffic.Uplink {
			entry.UplinkBytes, entry.HasUplink = counter.GetValue(), true
		} else {
			entry.DownlinkBytes, entry.HasDownlink = counter.GetValue(), true
		}
	}
	return grouped
}

func splitTrafficCounter(key string) (string, traffic.Direction) {
	if name, ok := strings.CutSuffix(key, uplink); ok {
		return name, traffic.Uplink
	}
	if name, ok := strings.CutSuffix(key, downlink); ok {
		return name, traffic.Downlink
	}
	return "", ""
}
