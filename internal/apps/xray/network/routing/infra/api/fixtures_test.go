package api

import (
	"context"
	"net"

	"google.golang.org/grpc"
)

type connection struct {
	call   func(string, any, any) error
	calls  int
	noPeer bool
}

func (c *connection) Invoke(ctx context.Context, method string, input, output any, options ...grpc.CallOption) error {
	c.calls++
	if !c.noPeer {
		for _, o := range options {
			if p, ok := o.(grpc.PeerCallOption); ok {
				p.PeerAddr.Addr = &net.TCPAddr{Port: 1}
			}
		}
	}
	return c.call(method, input, output)
}
func (*connection) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("unexpected stream")
}
