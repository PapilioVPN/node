//go:build integration

package api_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	native "github.com/xtls/xray-core/app/observatory/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/PapilioVPN/node/internal/modules/xray/outbounds/domain"
	"github.com/PapilioVPN/node/internal/testutil"
)

// Controlled SDK responses verify Node's adapter, not engine probe execution.
type outboundServer struct {
	native.UnimplementedObservatoryServiceServer
	read func(context.Context) (*native.GetOutboundStatusResponse, error)
}

func TestOutboundNativeErrorsDoNotBecomeEmptyObservationsOrReplay(t *testing.T) {
	for _, code := range []codes.Code{codes.NotFound, codes.Unimplemented, codes.Unavailable, codes.ResourceExhausted, codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			address, counts := outboundAPI(t, outboundServer{read: func(context.Context) (*native.GetOutboundStatusResponse, error) {
				return nil, status.Error(code, "private native reason")
			}})
			result, err := apiClient(t, address, time.Second, 4096).ReadOutboundStatuses(t.Context())
			if status.Code(err) != code || result.Items != nil || !result.ObservedAt.IsZero() || counts.RPCs.Load() != 1 {
				t.Fatalf("native failure became facts/replayed: %+v %v", result, err)
			}
			if code == codes.Unimplemented && !errors.Is(err, domain.ErrOutboundObservationUnsupported) || code == codes.ResourceExhausted && !errors.Is(err, domain.ErrAPIResourceLimit) || code == codes.Canceled && !errors.Is(err, context.Canceled) || code == codes.DeadlineExceeded && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("native classified cause lost")
			}
		})
	}
	address, counts := outboundAPI(t, nil)
	result, err := apiClient(t, address, time.Second, 4096).ReadOutboundStatuses(t.Context())
	if !errors.Is(err, domain.ErrOutboundObservationUnsupported) || result.Items != nil || !result.ObservedAt.IsZero() || counts.RPCs.Load() != 1 {
		t.Fatal("absent service became empty successful observations")
	}
}

func TestOutboundMalformedOrUnrepresentedEnvelopesReturnNoPartialFacts(t *testing.T) {
	unknown := protowire.AppendVarint(protowire.AppendTag(nil, 200, protowire.VarintType), 1)
	outer := &native.GetOutboundStatusResponse{Status: &observatory.ObservationResult{}}
	outer.ProtoReflect().SetUnknown(unknown)
	inner := &native.GetOutboundStatusResponse{Status: &observatory.ObservationResult{}}
	inner.Status.ProtoReflect().SetUnknown(unknown)
	extended := &observatory.OutboundStatus{OutboundTag: "future"}
	extended.ProtoReflect().SetUnknown(unknown)
	for _, response := range []*native.GetOutboundStatusResponse{nil, {}, outer, inner, {Status: &observatory.ObservationResult{Status: []*observatory.OutboundStatus{{OutboundTag: "partial"}, extended}}}} {
		address, _ := outboundAPI(t, outboundServer{read: func(context.Context) (*native.GetOutboundStatusResponse, error) { return response, nil }})
		result, err := apiClient(t, address, time.Second, 4096).ReadOutboundStatuses(t.Context())
		if result.Items != nil || !result.ObservedAt.IsZero() || (!errors.Is(err, domain.ErrInvalidOutboundObservation) && !errors.Is(err, domain.ErrOutboundObservationUnsupported)) {
			t.Fatal("missing/unrepresented native fields became successful/partial observations")
		}
	}
}

func TestOutboundReceiveBoundsAndSharedAdmissionJoinInterruptedReads(t *testing.T) {
	address, counts := outboundAPI(t, outboundServer{read: func(context.Context) (*native.GetOutboundStatusResponse, error) {
		return &native.GetOutboundStatusResponse{Status: &observatory.ObservationResult{Status: []*observatory.OutboundStatus{{OutboundTag: "beyond receive ceiling"}}}}, nil
	}})
	result, err := apiClient(t, address, time.Second, 1).ReadOutboundStatuses(t.Context())
	if !errors.Is(err, domain.ErrAPIResourceLimit) || result.Items != nil || !result.ObservedAt.IsZero() || counts.RPCs.Load() != 1 {
		t.Fatal("receive limit became partial observations or replayed")
	}
	for _, mode := range []string{"cancel", "timeout", "close"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{}, 1)
			address, _ := outboundAPI(t, outboundServer{read: func(ctx context.Context) (*native.GetOutboundStatusResponse, error) {
				select {
				case entered <- struct{}{}:
				default:
				}
				<-ctx.Done()
				return nil, status.FromContextError(ctx.Err()).Err()
			}})
			client := apiClient(t, address, time.Second, 4096)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			var result domain.OutboundStatuses
			var readError error
			go func() { result, readError = client.ReadOutboundStatuses(ctx); close(done) }()
			defer func() { cancel(); _ = client.Close(); <-done }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("native observation never started")
			}
			if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, domain.ErrAPIBusy) {
				t.Fatal("observatory bypassed shared stats capacity")
			}
			if _, err := client.ReadOutboundStatuses(t.Context()); !errors.Is(err, domain.ErrAPIBusy) {
				t.Fatal("saturated observation queued work")
			}
			if mode == "cancel" {
				cancel()
			}
			if mode == "close" {
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("interrupted observation did not join")
			}
			if readError == nil || result.Items != nil || !result.ObservedAt.IsZero() || mode == "cancel" && !errors.Is(readError, context.Canceled) || mode == "timeout" && !errors.Is(readError, context.DeadlineExceeded) {
				t.Fatalf("interruption became facts/lost cause: %+v %v", result, readError)
			}
			if mode != "close" {
				short, stop := context.WithTimeout(t.Context(), 50*time.Millisecond)
				_, err := client.ReadOutboundStatuses(short)
				stop()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("capacity was not released or shorter deadline lost")
				}
			}
		})
	}
}

func (s outboundServer) GetOutboundStatus(ctx context.Context, _ *native.GetOutboundStatusRequest) (*native.GetOutboundStatusResponse, error) {
	return s.read(ctx)
}

func outboundAPI(t *testing.T, service native.ObservatoryServiceServer) (string, *testutil.XrayAPICounts) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	counts := &testutil.XrayAPICounts{}
	server := grpc.NewServer(grpc.StatsHandler(counts))
	if service != nil {
		native.RegisterObservatoryServiceServer(server, service)
	}
	stats.RegisterStatsServiceServer(server, testutil.XrayRuntimeServer{Read: func(context.Context) (*stats.SysStatsResponse, error) { return &stats.SysStatsResponse{}, nil }})
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); server.GracefulStop(); <-done })
	return listener.Addr().String(), counts
}

func TestOutboundObservationsUseOneCallAndRetainNativeFields(t *testing.T) {
	for _, count := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			items := make([]*observatory.OutboundStatus, count)
			for i := range items {
				items[i] = &observatory.OutboundStatus{OutboundTag: fmt.Sprint(" opaque>>>", i%7, " "), Alive: i%2 == 0, Delay: math.MaxInt64, LastErrorReason: "private engine cause", LastSeenTime: math.MinInt64, LastTryTime: math.MaxInt64}
				if i%2 == 0 {
					items[i].HealthPing = &observatory.HealthPingMeasurementResult{All: math.MaxInt64, Fail: math.MinInt64, Deviation: 17, Average: 23, Max: math.MaxInt64, Min: math.MinInt64}
				}
			}
			address, counts := outboundAPI(t, outboundServer{read: func(context.Context) (*native.GetOutboundStatusResponse, error) {
				return &native.GetOutboundStatusResponse{Status: &observatory.ObservationResult{Status: items}}, nil
			}})
			client := apiClient(t, address, time.Second, 256*1024)
			if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
				t.Fatal(err)
			}
			before, calls := time.Now(), counts.RPCs.Load()
			result, err := client.ReadOutboundStatuses(t.Context())
			if err != nil || len(result.Items) != count || result.ObservedAt.Before(before) || result.ObservedAt.After(time.Now()) || counts.RPCs.Load() != calls+1 || counts.Opened.Load() != 1 {
				t.Fatalf("outbound read failed/truncated/added native calls: %+v %v", result, err)
			}
			for i, item := range result.Items {
				n := items[i]
				if item.Tag != n.OutboundTag || item.Alive != n.Alive || item.DelayMS != n.Delay || item.LastErrorReason != n.LastErrorReason || item.LastSeenUnixSec != n.LastSeenTime || item.LastTryUnixSec != n.LastTryTime || (item.HealthPing == nil) != (n.HealthPing == nil) {
					t.Fatal("native identity/order/duplicates/units changed")
				}
				if h := item.HealthPing; h != nil && (h.TotalSamples != n.HealthPing.All || h.FailedSamples != n.HealthPing.Fail || h.DeviationNS != n.HealthPing.Deviation || h.AverageNS != n.HealthPing.Average || h.MaxNS != n.HealthPing.Max || h.MinNS != n.HealthPing.Min) {
					t.Fatal("native burst precision/units changed")
				}
			}
			if count > 0 {
				result.Items[0].Tag, result.Items[0].HealthPing.AverageNS = "caller-owned", 999
				again, err := client.ReadOutboundStatuses(t.Context())
				if err != nil || again.Items[0].Tag != items[0].OutboundTag || again.Items[0].HealthPing.AverageNS != items[0].HealthPing.Average {
					t.Fatal("observations aliased later native replies/cache")
				}
			}
		})
	}
}
