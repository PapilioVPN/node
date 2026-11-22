package api

import (
	"context"

	command "github.com/xtls/xray-core/app/observatory/command"
	"google.golang.org/grpc"
)

type connection struct {
	calls  int
	result *command.GetOutboundStatusResponse
	err    error
}

func (c *connection) Invoke(_ context.Context, method string, in, out any, options ...grpc.CallOption) error {
	c.calls++
	if method != command.ObservatoryService_GetOutboundStatus_FullMethodName {
		panic(method)
	}
	response := out.(*command.GetOutboundStatusResponse)
	response.Status = c.result.Status
	response.ProtoReflect().SetUnknown(append([]byte(nil), c.result.ProtoReflect().GetUnknown()...))
	return c.err
}
func (*connection) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("unexpected stream")
}
