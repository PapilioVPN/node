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

type trafficInstances interface {
	NativeConnection(id string) (*grpc.ClientConn, error)
}

type Traffic struct {
	instances trafficInstances
}

func NewTraffic(instances trafficInstances) *Traffic {
	return &Traffic{instances: instances}
}

func (client *Traffic) Read(ctx context.Context, id string, scope traffic.Scope, name string) (traffic.Usage, error) {
	key := string(scope) + ">>>" + name
	counters, err := client.query(ctx, id, key+">>>traffic>>>", namedTrafficOptions)
	if err != nil {
		return traffic.Usage{}, err
	}

	entry := traffic.Usage{Name: name}
	for _, counter := range counters {
		switch counter.GetName() {
		case key + uplink:
			entry.UplinkBytes, entry.HasUplink = counter.GetValue(), true
		case key + downlink:
			entry.DownlinkBytes, entry.HasDownlink = counter.GetValue(), true
		}
	}
	if !entry.HasUplink && !entry.HasDownlink {
		return traffic.Usage{}, traffic.ErrNotFound
	}
	return entry, nil
}

func (client *Traffic) List(ctx context.Context, id string, scope traffic.Scope) ([]traffic.Usage, error) {
	prefix := string(scope) + ">>>"
	counters, err := client.query(ctx, id, prefix, listTrafficOptions)
	if err != nil {
		return nil, err
	}
	return groupTraffic(prefix, counters), nil
}

// QueryStats scans synchronously without consulting its server context. The
// deadline still bounds the local wait and cancels the native gRPC stream.
type trafficReadContext struct{ context.Context }

func (trafficReadContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (client *Traffic) query(ctx context.Context, id, pattern string, opts []grpc.CallOption) ([]*TrafficCounter, error) {
	conn, err := client.instances.NativeConnection(id)
	if err != nil {
		return nil, err
	}
	var reply QueryStatsResponse
	if err := conn.Invoke(trafficReadContext{ctx}, queryStats, &QueryStatsRequest{Pattern: pattern}, &reply, opts...); err != nil {
		if cause := ctx.Err(); cause != nil {
			return nil, cause
		}
		if status.Code(err) == codes.ResourceExhausted {
			return nil, errors.Join(traffic.ErrTooLarge, err)
		}
		return nil, err
	}
	current, err := client.instances.NativeConnection(id)
	if err != nil {
		return nil, err
	}
	if current != conn {
		return nil, traffic.ErrUnavailable
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
		name, isUpload := strings.CutSuffix(key, uplink)
		if !isUpload {
			var isDownload bool
			name, isDownload = strings.CutSuffix(key, downlink)
			if !isDownload {
				continue
			}
		}
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
		if isUpload {
			entry.UplinkBytes, entry.HasUplink = counter.GetValue(), true
		} else {
			entry.DownlinkBytes, entry.HasDownlink = counter.GetValue(), true
		}
	}
	return grouped
}
