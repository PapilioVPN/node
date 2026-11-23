// Package runtime collects live statistics from Xray.
package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/native"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	readTimeout = time.Second
	queueSize   = 128
)

type instances interface {
	NativeConnection(id string) (*grpc.ClientConn, error)
}

type readResult struct {
	snapshot Snapshot
	err      error
}

type readRequest struct {
	ctx   context.Context
	id    string
	conn  *grpc.ClientConn
	reply chan readResult
}

type Service struct {
	instances instances
	read      func(context.Context, *grpc.ClientConn) (Snapshot, error)
	native    chan struct{}
	queue     chan readRequest
	stop      context.CancelFunc
	ctx       context.Context
	done      chan struct{}
}

func New(instances instances) *Service {
	return newService(instances, readNative)
}

func newService(instances instances, read func(context.Context, *grpc.ClientConn) (Snapshot, error)) *Service {
	ctx, stop := context.WithCancel(context.Background())
	service := &Service{
		instances: instances, read: read, native: make(chan struct{}, 1),
		queue: make(chan readRequest, queueSize),
		ctx:   ctx, stop: stop, done: make(chan struct{}),
	}
	go service.collect()
	return service
}

func (service *Service) GetRuntimeStats(ctx context.Context, id string) (Snapshot, error) {
	conn, err := service.instances.NativeConnection(id)
	if err != nil {
		return Snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if service.ctx.Err() != nil {
		return Snapshot{}, ErrUnavailable
	}

	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	select {
	case service.native <- struct{}{}:
		defer func() { <-service.native }()
		if service.ctx.Err() != nil {
			return Snapshot{}, ErrUnavailable
		}
		result := service.sample(ctx, id, conn)
		return result.snapshot, result.err
	default:
	}
	reply := make(chan readResult, 1)
	select {
	case service.queue <- readRequest{ctx: ctx, id: id, conn: conn, reply: reply}:
	default:
		return Snapshot{}, ErrBusy
	}
	select {
	case result := <-reply:
		return result.snapshot, result.err
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case <-service.ctx.Done():
		return Snapshot{}, ErrUnavailable
	}
}

// Close cancels the active read and joins the collector.
func (service *Service) Close(ctx context.Context) error {
	service.stop()
	select {
	case <-service.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case service.native <- struct{}{}:
		<-service.native
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (service *Service) collect() {
	defer close(service.done)
	var group [queueSize + 1]readRequest
	for service.ctx.Err() == nil {
		select {
		case <-service.ctx.Done():
			return
		case group[0] = <-service.queue:
		}
		select {
		case <-service.ctx.Done():
			return
		case service.native <- struct{}{}:
		}
		// Cut the group before the native read begins, so late arrivals get a
		// fresh sample of their own.
		count := 1 + len(service.queue)
		for i := 1; i < count; i++ {
			group[i] = <-service.queue
		}
		service.collectGroup(group[:count])
		<-service.native
		clear(group[:count])
	}
}

func (service *Service) collectGroup(group []readRequest) {
	id := group[0].id
	conn, err := service.instances.NativeConnection(id)
	result := readResult{err: err}
	if err == nil {
		result.err = context.Canceled
		for _, request := range group {
			if request.conn == conn && request.ctx.Err() == nil {
				ctx, cancel := context.WithTimeout(service.ctx, readTimeout)
				result = service.sample(ctx, id, conn)
				cancel()
				break
			}
		}
	}
	for _, request := range group {
		if request.conn != conn && err == nil {
			request.reply <- readResult{err: ErrUnavailable}
		} else {
			request.reply <- result
		}
	}
}

func (service *Service) sample(ctx context.Context, id string, conn *grpc.ClientConn) readResult {
	snapshot, err := service.read(ctx, conn)
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return readResult{err: cause}
		}
		return readResult{err: errors.Join(ErrUnavailable, err)}
	}
	current, err := service.instances.NativeConnection(id)
	if err != nil {
		return readResult{err: err}
	}
	if current != conn {
		return readResult{err: ErrUnavailable}
	}
	return readResult{snapshot: snapshot}
}

// The caller enforces the timeout and cancels the Unix stream. Xray's
// GetSysStats performs a synchronous read, so it needs no separate wire timer.
type localRead struct{ context.Context }

func (localRead) Deadline() (time.Time, bool) { return time.Time{}, false }

var (
	nativeRequest emptypb.Empty
	nativeOptions = []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(1024)}
)

func readNative(ctx context.Context, conn *grpc.ClientConn) (Snapshot, error) {
	var result native.SysStatsResponse
	if err := conn.Invoke(localRead{ctx}, native.GetSysStats, &nativeRequest, &result, nativeOptions...); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Goroutines: result.GetNumGoroutine(), GCCount: result.GetNumGc(),
		HeapAllocatedBytes: result.GetAlloc(), TotalAllocatedBytes: result.GetTotalAlloc(),
		SystemBytes: result.GetSys(), Mallocs: result.GetMallocs(),
		Frees: result.GetFrees(), LiveObjects: result.GetLiveObjects(),
		GCPauseTotalNanoseconds: result.GetPauseTotalNs(), UptimeSeconds: result.GetUptime(),
	}, nil
}
