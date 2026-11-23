// Package runtime reads statistics for one running Xray instance.
package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/native"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type instances interface {
	NativeConnection(id string) (*grpc.ClientConn, error)
}

const maxConcurrentReads = 32

type Service struct {
	instances instances
	active    chan struct{}
}

func New(instances instances) *Service {
	return &Service{instances: instances, active: make(chan struct{}, maxConcurrentReads)}
}

func (service *Service) GetRuntimeStats(ctx context.Context, id string) (Snapshot, error) {
	conn, err := service.instances.NativeConnection(id)
	if err != nil {
		return Snapshot{}, err
	}
	select {
	case service.active <- struct{}{}:
		defer func() { <-service.active }()
	default:
		return Snapshot{}, ErrBusy
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var result native.SysStatsResponse
	if err := conn.Invoke(ctx, native.GetSysStats, &emptypb.Empty{}, &result,
		grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(1024)); err != nil {
		if cause := ctx.Err(); cause != nil {
			return Snapshot{}, cause
		}
		return Snapshot{}, errors.Join(ErrUnavailable, err)
	}

	// A stopped or replaced execution cannot serve a sample for the current one.
	current, err := service.instances.NativeConnection(id)
	if err != nil {
		return Snapshot{}, err
	}
	if current != conn {
		return Snapshot{}, ErrUnavailable
	}
	return Snapshot{
		Goroutines: result.GetNumGoroutine(), GCCount: result.GetNumGc(),
		HeapAllocatedBytes: result.GetAlloc(), TotalAllocatedBytes: result.GetTotalAlloc(),
		SystemBytes: result.GetSys(), Mallocs: result.GetMallocs(),
		Frees: result.GetFrees(), LiveObjects: result.GetLiveObjects(),
		GCPauseTotalNanoseconds: result.GetPauseTotalNs(), UptimeSeconds: result.GetUptime(),
	}, nil
}
