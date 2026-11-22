//go:build integration

package api

import (
	"context"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	reflection "google.golang.org/grpc/reflection/grpc_reflection_v1"
)

type boundedServer struct {
	stats.UnimplementedStatsServiceServer
	reflection.UnimplementedServerReflectionServer
	done chan struct{}
}

func (s *boundedServer) GetSysStats(context.Context, *stats.SysStatsRequest) (*stats.SysStatsResponse, error) {
	return &stats.SysStatsResponse{}, nil
}
func (s *boundedServer) ServerReflectionInfo(stream grpc.BidiStreamingServer[reflection.ServerReflectionRequest, reflection.ServerReflectionResponse]) error {
	defer close(s.done)
	if _, err := stream.Recv(); err != nil {
		return err
	}
	response := &reflection.ServerReflectionResponse{MessageResponse: &reflection.ServerReflectionResponse_ListServicesResponse{ListServicesResponse: &reflection.ListServiceResponse{Service: []*reflection.ServiceResponse{{Name: strings.Repeat("private", domain.MaxNativeBytes)}}}}}
	_ = stream.Send(response)
	<-stream.Context().Done()
	return stream.Context().Err()
}
