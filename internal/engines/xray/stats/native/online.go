package native

import (
	"context"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/online"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	getOnlineCount = "/xray.app.stats.command.StatsService/GetStatsOnline"
	getOnlineIPs   = "/xray.app.stats.command.StatsService/GetStatsOnlineIpList"
)

var (
	onlineCountOptions = []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(8 << 10)}
	onlineIPsOptions   = []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(1 << 20)}
)

type Online struct{ instances activeInstances }

func NewOnline(instances activeInstances) *Online {
	return &Online{instances: instances}
}

func (client *Online) Count(ctx context.Context, id, name string) (int64, error) {
	var result OnlineCountResponse
	key := onlineKey(name)
	if err := client.read(ctx, id, key, getOnlineCount, &result, onlineCountOptions); err != nil {
		return 0, err
	}
	if result.GetStat().GetName() != key || result.GetStat().GetValue() < 0 {
		return 0, online.ErrUnavailable
	}
	return result.Stat.Value, nil
}

func (client *Online) IPs(ctx context.Context, id, name string) (map[string]int64, error) {
	var result OnlineIPsResponse
	key := onlineKey(name)
	if err := client.read(ctx, id, key, getOnlineIPs, &result, onlineIPsOptions); err != nil {
		return nil, err
	}
	if result.Name != key {
		return nil, online.ErrUnavailable
	}
	return result.Ips, nil
}

func onlineKey(name string) string { return "user>>>" + name + ">>>online" }

func (client *Online) read(ctx context.Context, id, key, method string, response proto.Message, opts []grpc.CallOption) error {
	conn, err := client.instances.NativeConnection(id)
	if err != nil {
		return err
	}
	err = conn.Invoke(statsReadContext{ctx}, method, &OnlineRequest{Name: key}, response, opts...)
	if cause := ctx.Err(); cause != nil {
		return cause
	}
	current, stateErr := client.instances.NativeConnection(id)
	if stateErr != nil {
		return stateErr
	}
	if current != conn {
		return online.ErrUnavailable
	}
	switch status.Code(err) {
	case codes.OK:
		return nil
	case codes.NotFound:
		return online.ErrNotFound
	case codes.ResourceExhausted:
		return online.ErrTooLarge
	default:
		return err
	}
}
