//go:build integration

package xray_test

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

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
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
	address, connections := apiServer(t, trafficServer{read: func(_ context.Context, request *native.GetStatsRequest) (*native.GetStatsResponse, error) {
		calls.Add(1)
		value := expected.Load()
		if request.Name != value.Name || request.Reset_ {
			return nil, status.Error(codes.InvalidArgument, "incorrect selection or reset")
		}
		return &native.GetStatsResponse{Stat: &native.Stat{Name: request.Name, Value: value.Value}}, nil
	}})
	client := apiClient(t, address, time.Second, 4096)
	for _, scope := range []struct {
		value domain.TrafficScope
		name  string
	}{{domain.UserTraffic, "user>>> Mixed@Example >>>traffic>>>"}, {domain.InboundTraffic, "inbound>>> Mixed@Example >>>traffic>>>"}, {domain.OutboundTraffic, "outbound>>> Mixed@Example >>>traffic>>>"}} {
		for _, direction := range []domain.TrafficDirection{domain.Uplink, domain.Downlink} {
			for _, value := range []int64{0, math.MaxInt64, math.MinInt64} {
				expected.Store(&native.Stat{Name: scope.name + string(direction), Value: value})
				before := time.Now()
				counter, err := client.ReadTrafficCounter(t.Context(), domain.TrafficSelection{Scope: scope.value, Name: " Mixed@Example ", Direction: direction})
				if err != nil || counter.Bytes != value || counter.ObservedAt.Before(before) || counter.ObservedAt.After(time.Now()) {
					t.Fatalf("counter facts: %+v, %v", counter, err)
				}
			}
		}
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 18 || connections.opened.Load() != 1 {
		t.Fatalf("native calls=%d, connections=%d", calls.Load(), connections.opened.Load())
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
		{"missing", nil, status.Error(codes.NotFound, "private counter"), 4096, domain.ErrTrafficCounterNotFound, codes.NotFound},
		{"unsupported", nil, status.Error(codes.Unimplemented, "private method"), 4096, domain.ErrTrafficUnsupported, codes.Unimplemented},
		{"empty", &native.GetStatsResponse{}, nil, 4096, domain.ErrInvalidTrafficResponse, codes.Unknown},
		{"wrong-name", &native.GetStatsResponse{Stat: &native.Stat{Name: "another counter", Value: 99}}, nil, 4096, domain.ErrInvalidTrafficResponse, codes.Unknown},
		{"limit", &native.GetStatsResponse{Stat: &native.Stat{Name: "inbound>>>proxy>>>traffic>>>uplink", Value: 99}}, nil, 1, domain.ErrAPIResourceLimit, codes.ResourceExhausted},
	} {
		t.Run(test.name, func(t *testing.T) {
			address, _ := apiServer(t, trafficServer{read: func(context.Context, *native.GetStatsRequest) (*native.GetStatsResponse, error) {
				return test.response, test.nativeError
			}})
			counter, err := apiClient(t, address, time.Second, test.limit).ReadTrafficCounter(t.Context(), domain.TrafficSelection{Scope: domain.InboundTraffic, Name: "proxy", Direction: domain.Uplink})
			if counter != (domain.TrafficCounter{}) || !errors.Is(err, test.cause) || status.Code(err) != test.code {
				t.Fatalf("native failure changed: %+v, %v", counter, err)
			}
		})
	}
}

func TestTrafficSharesAdmissionAndReleasesItAfterCancellation(t *testing.T) {
	started := make(chan struct{}, 1)
	address, _ := apiServer(t, trafficServer{read: func(ctx context.Context, _ *native.GetStatsRequest) (*native.GetStatsResponse, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}})
	client := apiClient(t, address, time.Second, 4096)
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan struct{})
	var readError error
	go func() {
		_, readError = client.ReadTrafficCounter(ctx, domain.TrafficSelection{Scope: domain.UserTraffic, Name: "test", Direction: domain.Uplink})
		close(finished)
	}()
	defer func() { cancel(); _ = client.Close(); <-finished }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("native read did not start")
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, domain.ErrAPIBusy) {
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
			address, _ := apiServer(t, trafficServer{read: func(ctx context.Context, _ *native.GetStatsRequest) (*native.GetStatsResponse, error) {
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
			var counter domain.TrafficCounter
			var readError error
			go func() {
				counter, readError = client.ReadTrafficCounter(ctx, domain.TrafficSelection{Scope: domain.InboundTraffic, Name: "proxy", Direction: domain.Uplink})
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
			if counter != (domain.TrafficCounter{}) || readError == nil || mode != "close" && !errors.Is(readError, context.DeadlineExceeded) {
				t.Fatalf("interrupted read became facts: %+v, %v", counter, readError)
			}
		})
	}
}
