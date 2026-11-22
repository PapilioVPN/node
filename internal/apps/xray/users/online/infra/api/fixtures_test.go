package api

import (
	"context"

	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type connection struct {
	calls  int
	method string
	name   string
	reply  proto.Message
	err    error
}

func (c *connection) Invoke(_ context.Context, method string, input, output any, _ ...grpc.CallOption) error {
	c.calls++
	c.method = method
	request := input.(*stats.GetStatsRequest)
	c.name = request.Name
	if request.Reset_ {
		panic("online reads must never reset")
	}
	if c.err != nil {
		return c.err
	}
	proto.Merge(output.(proto.Message), c.reply)
	return nil
}
func (*connection) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("unexpected stream")
}

type bulkConnection struct {
	calls   []string
	names   *stats.GetAllOnlineUsersResponse
	details *stats.GetUsersStatsResponse
}

func (c *bulkConnection) Invoke(_ context.Context, method string, input, output any, _ ...grpc.CallOption) error {
	c.calls = append(c.calls, method)
	switch method {
	case stats.StatsService_GetAllOnlineUsers_FullMethodName:
		proto.Merge(output.(proto.Message), c.names)
	case stats.StatsService_GetUsersStats_FullMethodName:
		r := input.(*stats.GetUsersStatsRequest)
		if r.IncludeTraffic || r.Reset_ {
			panic("presence must not read or reset traffic")
		}
		proto.Merge(output.(proto.Message), c.details)
	default:
		panic("unexpected per-user RPC")
	}
	return nil
}
func (*bulkConnection) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("unexpected stream")
}
