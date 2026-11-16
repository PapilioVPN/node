//go:build integration

package api_test

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	nativestats "github.com/xtls/xray-core/app/stats"
	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/xray/domain"
	trafficdomain "github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
)

func TestResetUsesNativeAtomicSwapWithoutCrossingCounterIdentity(t *testing.T) {
	manager, err := nativestats.NewManager(context.Background(), &nativestats.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	address, connections := apiServer(t, native.NewStatsServer(manager))
	client := apiClient(t, address, time.Second, 4096)
	for _, scope := range []trafficdomain.TrafficScope{trafficdomain.UserTraffic, trafficdomain.InboundTraffic, trafficdomain.OutboundTraffic} {
		for _, direction := range []trafficdomain.TrafficDirection{trafficdomain.Uplink, trafficdomain.Downlink} {
			selection := trafficdomain.TrafficSelection{Scope: scope, Name: " exact>>>opaque ", Direction: direction}
			name := string(scope) + ">>>" + selection.Name + ">>>traffic>>>" + string(direction)
			counter, err := manager.RegisterCounter(name)
			if err != nil {
				t.Fatal(err)
			}
			other, err := manager.RegisterCounter(name + "-other")
			if err != nil {
				t.Fatal(err)
			}
			other.Set(77)
			for _, value := range []int64{0, math.MinInt64, math.MaxInt64} {
				counter.Set(value)
				before := time.Now()
				result, err := client.ResetTrafficCounter(t.Context(), selection)
				if err != nil || result.PreviousBytes != value || result.AcknowledgedAt.Before(before) || result.AcknowledgedAt.After(time.Now()) {
					t.Fatalf("reset acknowledgement: %+v %v", result, err)
				}
				if counter.Value() != 0 || other.Value() != 77 {
					t.Fatal("reset did not apply exactly to its counter")
				}
			}
		}
	}
	if connections.opened.Load() != 1 {
		t.Fatal("reset constructed additional native channels")
	}
}

func TestResetFailureDistinguishesNoEffectFromUnknownOutcome(t *testing.T) {
	for _, entry := range []struct {
		name     string
		reply    *native.GetStatsResponse
		cause    error
		limit    int
		unknown  bool
		expected error
	}{
		{name: "missing", cause: status.Error(codes.NotFound, "private-counter"), limit: 4096, expected: trafficdomain.ErrTrafficCounterNotFound},
		{name: "unsupported", cause: status.Error(codes.Unimplemented, "private-method"), limit: 4096, expected: trafficdomain.ErrTrafficUnsupported},
		{name: "unavailable", cause: status.Error(codes.Unavailable, "private-endpoint"), limit: 4096, unknown: true},
		{name: "remote-canceled", cause: status.Error(codes.Canceled, "private"), limit: 4096, unknown: true, expected: context.Canceled},
		{name: "remote-deadline", cause: status.Error(codes.DeadlineExceeded, "private"), limit: 4096, unknown: true, expected: context.DeadlineExceeded},
		{name: "empty", reply: &native.GetStatsResponse{}, limit: 4096, unknown: true, expected: trafficdomain.ErrInvalidTrafficResponse},
		{name: "wrong-name", reply: &native.GetStatsResponse{Stat: &native.Stat{Name: "other"}}, limit: 4096, unknown: true, expected: trafficdomain.ErrInvalidTrafficResponse},
		{name: "reply-limit", reply: &native.GetStatsResponse{Stat: &native.Stat{Name: "inbound>>>proxy>>>traffic>>>uplink", Value: 99}}, limit: 1, unknown: true, expected: domain.ErrAPIResourceLimit},
	} {
		t.Run(entry.name, func(t *testing.T) {
			var calls atomic.Int32
			address, _ := apiServer(t, trafficServer{read: func(_ context.Context, request *native.GetStatsRequest) (*native.GetStatsResponse, error) {
				calls.Add(1)
				if !request.Reset_ || request.Name != "inbound>>>proxy>>>traffic>>>uplink" {
					t.Error("reset changed native selection or omitted reset")
				}
				return entry.reply, entry.cause
			}})
			result, err := apiClient(t, address, time.Second, entry.limit).ResetTrafficCounter(t.Context(), trafficdomain.TrafficSelection{Scope: trafficdomain.InboundTraffic, Name: "proxy", Direction: trafficdomain.Uplink})
			if err == nil || result != (trafficdomain.TrafficCounterReset{}) || errors.Is(err, trafficdomain.ErrTrafficResetUnknown) != entry.unknown || entry.expected != nil && !errors.Is(err, entry.expected) {
				t.Fatalf("reset outcome/cause changed: %+v %v", result, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("native reset retried: %d", calls.Load())
			}
		})
	}
}

func TestResetDeadlineCanFollowAnAppliedNativeEffect(t *testing.T) {
	var value atomic.Int64
	value.Store(123)
	var calls atomic.Int32
	entered := make(chan struct{})
	address, _ := apiServer(t, trafficServer{read: func(ctx context.Context, request *native.GetStatsRequest) (*native.GetStatsResponse, error) {
		calls.Add(1)
		value.Swap(0)
		close(entered)
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}})
	client := apiClient(t, address, 100*time.Millisecond, 4096)
	result, err := client.ResetTrafficCounter(t.Context(), trafficdomain.TrafficSelection{Scope: trafficdomain.UserTraffic, Name: "user", Direction: trafficdomain.Downlink})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, trafficdomain.ErrTrafficResetUnknown) || result != (trafficdomain.TrafficCounterReset{}) {
		t.Fatalf("lost unknown effect: %+v %v", result, err)
	}
	select {
	case <-entered:
	default:
		t.Fatal("reset never reached native server")
	}
	if calls.Load() != 1 || value.Load() != 0 {
		t.Fatal("reset was retried or effect was incorrectly undone")
	}
}

func TestResetSharesAdmissionAndRejectsKnownUnattemptedWork(t *testing.T) {
	entered := make(chan struct{}, 1)
	var calls atomic.Int32
	address, _ := apiServer(t, trafficServer{read: func(ctx context.Context, _ *native.GetStatsRequest) (*native.GetStatsResponse, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}})
	client := apiClient(t, address, time.Second, 4096)
	selection := trafficdomain.TrafficSelection{Scope: trafficdomain.UserTraffic, Name: "user", Direction: trafficdomain.Uplink}
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan error, 1)
	go func() { _, err := client.ReadTrafficCounter(ctx, selection); finished <- err }()
	defer cancel()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("native read did not start")
	}
	if _, err := client.ResetTrafficCounter(t.Context(), selection); !errors.Is(err, domain.ErrAPIBusy) || errors.Is(err, trafficdomain.ErrTrafficResetUnknown) {
		t.Fatalf("rejected reset became an unknown effect: %v", err)
	}
	cancel()
	<-finished
	canceled, release := context.WithCancel(t.Context())
	release()
	if _, err := client.ResetTrafficCounter(canceled, selection); !errors.Is(err, context.Canceled) || errors.Is(err, trafficdomain.ErrTrafficResetUnknown) {
		t.Fatalf("pre-canceled reset became attempted: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ResetTrafficCounter(t.Context(), selection); err == nil || errors.Is(err, trafficdomain.ErrTrafficResetUnknown) {
		t.Fatalf("closed API became attempted: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("unattempted reset reached native server: %d", calls.Load())
	}
}
