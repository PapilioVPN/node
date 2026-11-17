//go:build integration

package api_test

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	trafficdomain "github.com/PapilioVPN/node/internal/apps/xray/network/traffic/domain"
	"github.com/PapilioVPN/node/internal/shared/engineapi"
	"github.com/PapilioVPN/node/internal/testutil"
)

// Controlled native servers verify SDK requests; real Xray runs in bootstrap tests.
type trafficServer struct {
	native.UnimplementedStatsServiceServer
	read func(context.Context, *native.GetStatsRequest) (*native.GetStatsResponse, error)
}

func (s trafficServer) GetStats(ctx context.Context, request *native.GetStatsRequest) (*native.GetStatsResponse, error) {
	return s.read(ctx, request)
}

func (trafficServer) GetSysStats(context.Context, *native.SysStatsRequest) (*native.SysStatsResponse, error) {
	return &native.SysStatsResponse{NumGoroutine: 1}, nil
}

func TestTrafficReadsOneExactCounterWithoutResetAndSharesRuntimeChannel(t *testing.T) {
	var calls atomic.Int32
	var expected atomic.Pointer[native.Stat]
	address, connections := testutil.XrayStatsAPI(t, trafficServer{read: func(_ context.Context, request *native.GetStatsRequest) (*native.GetStatsResponse, error) {
		calls.Add(1)
		value := expected.Load()
		if request.Name != value.Name || request.Reset_ {
			return nil, status.Error(codes.InvalidArgument, "incorrect selection or reset")
		}
		return &native.GetStatsResponse{Stat: &native.Stat{Name: request.Name, Value: value.Value}}, nil
	}})
	client := apiClient(t, address, time.Second, 4096)
	for _, scope := range []struct {
		value trafficdomain.TrafficScope
		name  string
	}{{trafficdomain.UserTraffic, "user>>> Mixed@Example >>>traffic>>>"}, {trafficdomain.InboundTraffic, "inbound>>> Mixed@Example >>>traffic>>>"}, {trafficdomain.OutboundTraffic, "outbound>>> Mixed@Example >>>traffic>>>"}} {
		for _, direction := range []trafficdomain.TrafficDirection{trafficdomain.Uplink, trafficdomain.Downlink} {
			for _, value := range []int64{0, math.MaxInt64, math.MinInt64} {
				expected.Store(&native.Stat{Name: scope.name + string(direction), Value: value})
				before := time.Now()
				counter, err := client.ReadTrafficCounter(t.Context(), trafficdomain.TrafficSelection{Scope: scope.value, Name: " Mixed@Example ", Direction: direction})
				if err != nil || counter.Bytes != value || counter.ObservedAt.Before(before) || counter.ObservedAt.After(time.Now()) {
					t.Fatalf("counter facts: %+v, %v", counter, err)
				}
			}
		}
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 18 || connections.Opened.Load() != 1 {
		t.Fatalf("native calls=%d, connections=%d", calls.Load(), connections.Opened.Load())
	}
}

func TestTrafficNativeAbsenceUnsupportedMalformedAndBoundedResponses(t *testing.T) {
	for _, test := range []struct {
		name        string
		response    *native.GetStatsResponse
		nativeError error
		limit       int
		cause       error
		code        codes.Code
	}{
		{"missing", nil, status.Error(codes.NotFound, "private counter"), 4096, trafficdomain.ErrTrafficCounterNotFound, codes.NotFound},
		{"unsupported", nil, status.Error(codes.Unimplemented, "private method"), 4096, trafficdomain.ErrTrafficUnsupported, codes.Unimplemented},
		{"empty", &native.GetStatsResponse{}, nil, 4096, trafficdomain.ErrInvalidTrafficResponse, codes.Unknown},
		{"wrong-name", &native.GetStatsResponse{Stat: &native.Stat{Name: "another counter", Value: 99}}, nil, 4096, trafficdomain.ErrInvalidTrafficResponse, codes.Unknown},
		{"limit", &native.GetStatsResponse{Stat: &native.Stat{Name: "inbound>>>proxy>>>traffic>>>uplink", Value: 99}}, nil, 1, engineapi.ErrResourceLimit, codes.ResourceExhausted},
	} {
		t.Run(test.name, func(t *testing.T) {
			address, _ := testutil.XrayStatsAPI(t, trafficServer{read: func(context.Context, *native.GetStatsRequest) (*native.GetStatsResponse, error) {
				return test.response, test.nativeError
			}})
			counter, err := apiClient(t, address, time.Second, test.limit).ReadTrafficCounter(t.Context(), trafficdomain.TrafficSelection{Scope: trafficdomain.InboundTraffic, Name: "proxy", Direction: trafficdomain.Uplink})
			if counter != (trafficdomain.TrafficCounter{}) || !errors.Is(err, test.cause) || status.Code(err) != test.code {
				t.Fatalf("native failure changed: %+v, %v", counter, err)
			}
		})
	}
}

func TestTrafficSharesAdmissionAndReleasesItAfterCancellation(t *testing.T) {
	started := make(chan struct{}, 1)
	address, _ := testutil.XrayStatsAPI(t, trafficServer{read: func(ctx context.Context, _ *native.GetStatsRequest) (*native.GetStatsResponse, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}})
	client := apiClient(t, address, time.Second, 4096)
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan struct{})
	var readError error
	go func() {
		_, readError = client.ReadTrafficCounter(ctx, trafficdomain.TrafficSelection{Scope: trafficdomain.UserTraffic, Name: "test", Direction: trafficdomain.Uplink})
		close(finished)
	}()
	defer func() { cancel(); _ = client.Close(); <-finished }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("native read did not start")
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, engineapi.ErrBusy) {
		t.Fatalf("runtime bypassed shared capacity: %v", err)
	}
	cancel()
	<-finished
	if !errors.Is(readError, context.Canceled) {
		t.Fatalf("lost cancellation: %v", readError)
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
		t.Fatalf("capacity not released: %v", err)
	}
}

func TestTrafficNativeDeadlineAndCloseDiscardFacts(t *testing.T) {
	for _, mode := range []string{"deadline", "native-timeout", "close"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{}, 1)
			address, _ := testutil.XrayStatsAPI(t, trafficServer{read: func(ctx context.Context, _ *native.GetStatsRequest) (*native.GetStatsResponse, error) {
				started <- struct{}{}
				<-ctx.Done()
				return nil, status.FromContextError(ctx.Err()).Err()
			}})
			timeout := 2 * time.Second
			if mode == "native-timeout" {
				timeout = time.Second
			}
			client := apiClient(t, address, timeout, 4096)
			ctx, cancel := context.WithCancel(t.Context())
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), time.Second)
			}
			finished := make(chan struct{})
			var counter trafficdomain.TrafficCounter
			var readError error
			go func() {
				counter, readError = client.ReadTrafficCounter(ctx, trafficdomain.TrafficSelection{Scope: trafficdomain.InboundTraffic, Name: "proxy", Direction: trafficdomain.Uplink})
				close(finished)
			}()
			defer func() { cancel(); _ = client.Close(); <-finished }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("native read did not start")
			}
			if mode == "close" {
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
			}
			<-finished
			if counter != (trafficdomain.TrafficCounter{}) || readError == nil || mode != "close" && !errors.Is(readError, context.DeadlineExceeded) {
				t.Fatalf("interrupted read became facts: %+v, %v", counter, readError)
			}
		})
	}
}
