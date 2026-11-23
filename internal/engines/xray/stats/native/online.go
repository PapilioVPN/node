package native

import (
	"context"
	"strings"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/online"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	getOnlineCount = "/xray.app.stats.command.StatsService/GetStatsOnline"
	getOnlineIPs   = "/xray.app.stats.command.StatsService/GetStatsOnlineIpList"
	getOnlineUsers = "/xray.app.stats.command.StatsService/GetAllOnlineUsers"
	onlinePrefix   = "user>>>"
	onlineSuffix   = ">>>online"
)

var (
	onlineCountOptions = []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(8 << 10)}
	onlineListOptions  = []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(1 << 20)}
)

type Online struct{ instances activeInstances }

func NewOnline(instances activeInstances) *Online {
	return &Online{instances: instances}
}

func (client *Online) Count(ctx context.Context, id, name string) (int64, error) {
	var result NamedCounterResponse
	key := onlineKey(name)
	if err := client.read(ctx, id, getOnlineCount, &OnlineRequest{Name: key}, &result, onlineCountOptions); err != nil {
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
	if err := client.read(ctx, id, getOnlineIPs, &OnlineRequest{Name: key}, &result, onlineListOptions); err != nil {
		return nil, err
	}
	if result.Name != key {
		return nil, online.ErrUnavailable
	}
	return result.Ips, nil
}

func (client *Online) Users(ctx context.Context, id string) ([]string, error) {
	var result OnlineUsersResponse
	if err := client.read(ctx, id, getOnlineUsers, &emptypb.Empty{}, &result, onlineListOptions); err != nil {
		return nil, err
	}
	for index, key := range result.Users {
		remainder, hasPrefix := strings.CutPrefix(key, onlinePrefix)
		name, hasSuffix := strings.CutSuffix(remainder, onlineSuffix)
		if !hasPrefix || !hasSuffix || name == "" {
			return nil, online.ErrUnavailable
		}
		result.Users[index] = name
	}
	return result.Users, nil
}

func onlineKey(name string) string { return onlinePrefix + name + onlineSuffix }

func (client *Online) read(ctx context.Context, id, method string, request, response proto.Message, opts []grpc.CallOption) error {
	conn, err := client.instances.NativeConnection(id)
	if err != nil {
		return err
	}
	err = conn.Invoke(localDeadlineContext{ctx}, method, request, response, opts...)
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
