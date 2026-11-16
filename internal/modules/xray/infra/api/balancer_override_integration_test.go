//go:build integration

package api_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/PapilioVPN/node/internal/modules/xray/domain"
	routingdomain "github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
)

type balancerOverrideServer struct {
	native.UnimplementedRoutingServiceServer
	override func(context.Context, *native.OverrideBalancerTargetRequest) (*native.OverrideBalancerTargetResponse, error)
}

func TestBalancerOverridePreservesExactTargetsAndDoesNotScanUnrelatedBalancers(t *testing.T) {
	for _, size := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var state sync.Mutex
			balancers := make(map[string]string, size+1)
			for i := 0; i < size; i++ {
				balancers[fmt.Sprint("unrelated-", i)] = "unchanged"
			}
			balancers[" exact>>>pool "] = "previous"
			address, counts := routingAPI(t, balancerOverrideServer{override: func(_ context.Context, r *native.OverrideBalancerTargetRequest) (*native.OverrideBalancerTargetResponse, error) {
				state.Lock()
				defer state.Unlock()
				if _, ok := balancers[r.BalancerTag]; !ok {
					return nil, status.Error(codes.Unknown, "wrong tag")
				}
				balancers[r.BalancerTag] = r.Target
				return &native.OverrideBalancerTargetResponse{}, nil
			}})
			client := apiClient(t, address, time.Second, 4096)
			for i, target := range []string{"", "   ", "not-an-existing-outbound", " outside>>>selector "} {
				ack, err := client.OverrideBalancerTarget(t.Context(), routingdomain.BalancerOverride{BalancerTag: " exact>>>pool ", Target: target})
				if err != nil || ack.AcknowledgedAt.IsZero() || counts.rpcs.Load() != int64(i+1) || counts.opened.Load() != 1 {
					t.Fatalf("override did extra preflight/scans or rejected native target: %+v %v", ack, err)
				}
				state.Lock()
				actual := balancers[" exact>>>pool "]
				state.Unlock()
				if actual != target {
					t.Fatalf("override target/clear intent changed: %q want %q", actual, target)
				}
			}
			if counts.rpcs.Load() != 4 {
				t.Fatal("override was replayed")
			}
			state.Lock()
			defer state.Unlock()
			for key, value := range balancers {
				if key != " exact>>>pool " && value != "unchanged" {
					t.Fatal("unrelated balancer was modified")
				}
			}
		})
	}
}

func TestBalancerOverrideFailureCodesRetainUncertainEffectsWithoutRetry(t *testing.T) {
	for _, code := range []codes.Code{codes.Unimplemented, codes.NotFound, codes.InvalidArgument, codes.Unknown, codes.Unavailable, codes.ResourceExhausted, codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			address, counts := routingAPI(t, balancerOverrideServer{override: func(context.Context, *native.OverrideBalancerTargetRequest) (*native.OverrideBalancerTargetResponse, error) {
				return nil, status.Error(code, "private diagnostics")
			}})
			ack, err := apiClient(t, address, time.Second, 4096).OverrideBalancerTarget(t.Context(), routingdomain.BalancerOverride{BalancerTag: "pool", Target: "target"})
			if err == nil || !ack.AcknowledgedAt.IsZero() || status.Code(err) != code || counts.rpcs.Load() != 1 || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) != (code != codes.Unimplemented) {
				t.Fatalf("native failure erased/replayed effects: %+v %v", ack, err)
			}
			if code == codes.Unimplemented && !errors.Is(err, routingdomain.ErrRoutingManagementUnsupported) {
				t.Fatal(err)
			}
			if code == codes.ResourceExhausted && !errors.Is(err, domain.ErrAPIResourceLimit) {
				t.Fatal(err)
			}
			if code == codes.Canceled && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if code == codes.DeadlineExceeded && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}

func TestBalancerOverrideReceiveLimitDoesNotPromiseNoEffects(t *testing.T) {
	var effects atomic.Int32
	response := &native.OverrideBalancerTargetResponse{}
	response.ProtoReflect().SetUnknown(protowire.AppendBytes(protowire.AppendTag(nil, 99, protowire.BytesType), []byte("oversized future metadata")))
	address, counts := routingAPI(t, balancerOverrideServer{override: func(context.Context, *native.OverrideBalancerTargetRequest) (*native.OverrideBalancerTargetResponse, error) {
		effects.Add(1)
		return response, nil
	}})
	ack, err := apiClient(t, address, time.Second, 1).OverrideBalancerTarget(t.Context(), routingdomain.BalancerOverride{BalancerTag: "pool"})
	if !ack.AcknowledgedAt.IsZero() || !errors.Is(err, routingdomain.ErrRoutingMutationUnknown) || !errors.Is(err, domain.ErrAPIResourceLimit) || effects.Load() != 1 || counts.rpcs.Load() != 1 {
		t.Fatalf("oversized acknowledgement became success/no-effect/replay: %+v %v", ack, err)
	}
}

func TestBalancerOverrideCanceledAfterEffectSharesCapacityAndJoins(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "close"} {
		t.Run(mode, func(t *testing.T) {
			var effects atomic.Int32
			entered := make(chan struct{})
			address, counts := routingAPI(t, balancerOverrideServer{override: func(ctx context.Context, _ *native.OverrideBalancerTargetRequest) (*native.OverrideBalancerTargetResponse, error) {
				effects.Add(1)
				close(entered)
				<-ctx.Done()
				return nil, status.FromContextError(ctx.Err()).Err()
			}})
			client := apiClient(t, address, time.Second, 4096)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			var mutationError error
			go func() {
				defer close(done)
				_, mutationError = client.OverrideBalancerTarget(ctx, routingdomain.BalancerOverride{BalancerTag: "pool", Target: "target"})
			}()
			defer func() { cancel(); _ = client.Close(); <-done }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("native override did not start")
			}
			if _, err := client.OverrideBalancerTarget(t.Context(), routingdomain.BalancerOverride{BalancerTag: "pool"}); !errors.Is(err, domain.ErrAPIBusy) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) {
				t.Fatalf("rejected override admitted effects: %v", err)
			}
			if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, domain.ErrAPIBusy) {
				t.Fatalf("runtime bypassed shared capacity: %v", err)
			}
			if _, err := client.ReadBalancerInfo(t.Context(), "pool"); !errors.Is(err, domain.ErrAPIBusy) {
				t.Fatalf("balancer read bypassed shared capacity: %v", err)
			}
			if _, err := client.TestRoute(t.Context(), routingdomain.RoutingProbe{}); !errors.Is(err, domain.ErrAPIBusy) {
				t.Fatalf("probe bypassed shared capacity: %v", err)
			}
			switch mode {
			case "cancel":
				cancel()
			case "close":
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
				if !errors.Is(mutationError, routingdomain.ErrRoutingMutationUnknown) || effects.Load() != 1 || counts.rpcs.Load() != 1 {
					t.Fatalf("lost acknowledgement erased/replayed effects: %v", mutationError)
				}
				if mode == "cancel" && !errors.Is(mutationError, context.Canceled) {
					t.Fatal(mutationError)
				}
				if mode == "deadline" && !errors.Is(mutationError, context.DeadlineExceeded) {
					t.Fatal(mutationError)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("override did not join after cancellation/close")
			}
		})
	}
}

func (s balancerOverrideServer) OverrideBalancerTarget(ctx context.Context, r *native.OverrideBalancerTargetRequest) (*native.OverrideBalancerTargetResponse, error) {
	return s.override(ctx, r)
}

func TestBalancerOverrideUsesOneExactRPCAndExistingChannel(t *testing.T) {
	address, counts := routingAPI(t, balancerOverrideServer{override: func(_ context.Context, r *native.OverrideBalancerTargetRequest) (*native.OverrideBalancerTargetResponse, error) {
		if r.BalancerTag != " exact>>>pool " || r.Target != " outside>>>selector " {
			t.Error("override identity/target changed")
		}
		return &native.OverrideBalancerTargetResponse{}, nil
	}})
	client := apiClient(t, address, time.Second, 4096)
	if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	ack, err := client.OverrideBalancerTarget(t.Context(), routingdomain.BalancerOverride{BalancerTag: " exact>>>pool ", Target: " outside>>>selector "})
	if err != nil || ack.AcknowledgedAt.Before(before) || ack.AcknowledgedAt.After(time.Now()) || counts.rpcs.Load() != 2 || counts.opened.Load() != 1 {
		t.Fatalf("override was unsupported, changed or added RPCs/channels: %+v %v", ack, err)
	}
}
