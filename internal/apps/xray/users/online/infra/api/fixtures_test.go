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
