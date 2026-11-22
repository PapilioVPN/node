//go:build integration

package api

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/xtls/xray-core/app/observatory"
	command "github.com/xtls/xray-core/app/observatory/command"
)

type boundedServer struct {
	command.UnimplementedObservatoryServiceServer
	size  atomic.Int32
	calls atomic.Int32
}

func (s *boundedServer) GetOutboundStatus(ctx context.Context, _ *command.GetOutboundStatusRequest) (*command.GetOutboundStatusResponse, error) {
	s.calls.Add(1)
	if s.size.Load() < 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &command.GetOutboundStatusResponse{Status: &observatory.ObservationResult{Status: []*observatory.OutboundStatus{{OutboundTag: "tag", LastErrorReason: strings.Repeat("p", int(s.size.Load()))}}}}, nil
}
