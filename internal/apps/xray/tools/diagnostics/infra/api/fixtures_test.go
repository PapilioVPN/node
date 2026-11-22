package api

import (
	"context"

	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	reflection "google.golang.org/grpc/reflection/grpc_reflection_v1"
)

type connection struct {
	response                    *reflection.ServerReflectionResponse
	calls, streams              int
	statsError, reflectionError error
	names                       []string
	sent                        bool
	closed                      bool
}

func (c *connection) Invoke(_ context.Context, method string, args, reply any, options ...grpc.CallOption) error {
	c.calls++
	if method != stats.StatsService_GetSysStats_FullMethodName {
		panic(method)
	}
	reply.(*stats.SysStatsResponse).Uptime = 123
	return c.statsError
}
func (c *connection) NewStream(ctx context.Context, _ *grpc.StreamDesc, method string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
	c.streams++
	return &stream{connection: c}, nil
}

type stream struct {
	grpc.ClientStream
	connection *connection
}

func (s *stream) SendMsg(message any) error {
	s.connection.sent = true
	if _, ok := message.(*reflection.ServerReflectionRequest).MessageRequest.(*reflection.ServerReflectionRequest_ListServices); !ok {
		panic("unexpected reflection operation")
	}
	return nil
}
func (s *stream) RecvMsg(message any) error {
	if s.connection.reflectionError != nil {
		return s.connection.reflectionError
	}
	if s.connection.response != nil {
		message.(*reflection.ServerReflectionResponse).MessageResponse = s.connection.response.MessageResponse
		return nil
	}
	list := &reflection.ListServiceResponse{}
	for _, name := range s.connection.names {
		list.Service = append(list.Service, &reflection.ServiceResponse{Name: name})
	}
	message.(*reflection.ServerReflectionResponse).MessageResponse = &reflection.ServerReflectionResponse_ListServicesResponse{ListServicesResponse: list}
	return nil
}
func (s *stream) CloseSend() error { s.connection.closed = true; return nil }
