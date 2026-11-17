//go:build integration

package api_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	routernative "github.com/xtls/xray-core/app/router"
	native "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"

	routingdomain "github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	"github.com/PapilioVPN/node/internal/shared/engineapi"
)

type ruleRemovalServer struct {
	native.UnimplementedRoutingServiceServer
	remove func(context.Context, *native.RemoveRuleRequest) (*native.RemoveRuleResponse, error)
}

func (s ruleRemovalServer) RemoveRule(ctx context.Context, r *native.RemoveRuleRequest) (*native.RemoveRuleResponse, error) {
	return s.remove(ctx, r)
}

// This runs the actual pinned router and canonical service, not a simulated rule map.
func TestRuleRemovalNativeRouterKeepsOrderWithOneCallAtEverySize(t *testing.T) {
	for _, size := range []int{0, 1, 100, 1000} {
		for _, tag := range []string{" exact>>>rule ", "   "} {
			t.Run(fmt.Sprintf("%d/%q", size, tag), func(t *testing.T) {
				config := &routernative.Config{}
				for i := 0; i < size; i++ {
					config.Rule = append(config.Rule, &routernative.RoutingRule{InboundTag: []string{"in"}, RuleTag: fmt.Sprint("unrelated-", size-i), TargetTag: &routernative.RoutingRule_Tag{Tag: fmt.Sprint("out-", i)}})
				}
				config.Rule = slices.Insert(config.Rule, size/2, &routernative.RoutingRule{InboundTag: []string{"in"}, RuleTag: tag, TargetTag: &routernative.RoutingRule_Tag{Tag: "removed-out"}})
				router := new(routernative.Router)
				if err := router.Init(t.Context(), config, nil, nil, nil); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := router.Close(); err != nil {
						t.Error(err)
					}
				})
				address, counts := routingAPI(t, native.NewRoutingServer(router, nil))
				client := apiClient(t, address, time.Second, 4096)
				if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
					t.Fatal(err)
				}
				for attempt := 0; attempt < 2; attempt++ {
					before := time.Now()
					ack, err := client.RemoveRoutingRule(t.Context(), tag)
					if err != nil || ack.AcknowledgedAt.Before(before) || ack.AcknowledgedAt.After(time.Now()) || counts.RPCs.Load() != int64(attempt+2) || counts.Opened.Load() != 1 {
						t.Fatalf("removal rejected missing tag/added preflight/replayed: %+v %v", ack, err)
					}
					actual := router.ListRule()
					if len(actual) != size {
						t.Fatalf("wrong native membership after removal: %d", len(actual))
					}
					for i, rule := range actual {
						if rule.GetRuleTag() != fmt.Sprint("unrelated-", size-i) || rule.GetOutboundTag() != fmt.Sprint("out-", i) {
							t.Fatal("unrelated order/tag changed")
						}
					}
				}
			})
		}
	}
}

func TestRuleRemovalNativeErrorsAndReceiveLossPreserveUnknownEffects(t *testing.T) {
	for _, code := range []codes.Code{codes.Unimplemented, codes.NotFound, codes.InvalidArgument, codes.Unknown, codes.Unavailable, codes.ResourceExhausted, codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			address, counts := routingAPI(t, ruleRemovalServer{remove: func(context.Context, *native.RemoveRuleRequest) (*native.RemoveRuleResponse, error) {
				return nil, status.Error(code, "private diagnostics")
			}})
			ack, err := apiClient(t, address, time.Second, 4096).RemoveRoutingRule(t.Context(), "rule")
			if err == nil || status.Code(err) != code || !ack.AcknowledgedAt.IsZero() || counts.RPCs.Load() != 1 || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) != (code != codes.Unimplemented) {
				t.Fatalf("native removal error lost/replayed effects: %+v %v", ack, err)
			}
			if code == codes.Unimplemented && !errors.Is(err, routingdomain.ErrRoutingManagementUnsupported) {
				t.Fatal(err)
			}
			if code == codes.ResourceExhausted && !errors.Is(err, engineapi.ErrResourceLimit) {
				t.Fatal(err)
			}
		})
	}
	var effects atomic.Int32
	response := &native.RemoveRuleResponse{}
	response.ProtoReflect().SetUnknown(protowire.AppendBytes(protowire.AppendTag(nil, 99, protowire.BytesType), []byte("oversized future metadata")))
	address, counts := routingAPI(t, ruleRemovalServer{remove: func(context.Context, *native.RemoveRuleRequest) (*native.RemoveRuleResponse, error) {
		effects.Add(1)
		return response, nil
	}})
	ack, err := apiClient(t, address, time.Second, 1).RemoveRoutingRule(t.Context(), "rule")
	if !ack.AcknowledgedAt.IsZero() || !errors.Is(err, routingdomain.ErrRoutingMutationUnknown) || !errors.Is(err, engineapi.ErrResourceLimit) || effects.Load() != 1 || counts.RPCs.Load() != 1 {
		t.Fatalf("receive loss claimed no effects/replayed: %+v %v", ack, err)
	}
}

func TestRuleRemovalSharesCapacityAndReleasesCallerOnCancellation(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "close"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			address, counts := routingAPI(t, ruleRemovalServer{remove: func(ctx context.Context, _ *native.RemoveRuleRequest) (*native.RemoveRuleResponse, error) {
				close(entered)
				<-ctx.Done()
				return nil, status.FromContextError(ctx.Err()).Err()
			}})
			client := apiClient(t, address, time.Second, 4096)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			var removalError error
			go func() { defer close(done); _, removalError = client.RemoveRoutingRule(ctx, "rule") }()
			defer func() { cancel(); _ = client.Close(); <-done }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("native removal did not start")
			}
			if _, err := client.RemoveRoutingRule(t.Context(), "other"); !errors.Is(err, engineapi.ErrBusy) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) {
				t.Fatalf("busy removal admitted effects: %v", err)
			}
			if _, err := client.OverrideBalancerTarget(t.Context(), routingdomain.BalancerOverride{BalancerTag: "pool"}); !errors.Is(err, engineapi.ErrBusy) {
				t.Fatal("override bypassed shared removal capacity")
			}
			if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, engineapi.ErrBusy) {
				t.Fatal("read bypassed shared removal capacity")
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
				err := removalError
				if !errors.Is(err, routingdomain.ErrRoutingMutationUnknown) || counts.RPCs.Load() != 1 {
					t.Fatalf("canceled/closed removal lost effects/replayed: %v", err)
				}
				if mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("removal caller was not released")
			}
		})
	}
}

type observingRuleRemovalServer struct {
	native.RoutingServiceServer
	finished chan struct{}
}

func (s observingRuleRemovalServer) RemoveRule(ctx context.Context, r *native.RemoveRuleRequest) (*native.RemoveRuleResponse, error) {
	defer close(s.finished)
	return s.RoutingServiceServer.RemoveRule(ctx, r)
}

func TestRuleRemovalNativeEffectPrecedesWebhookCleanupAndCanOutliveCaller(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(webhook.Close)
	router := new(routernative.Router)
	if err := router.Init(t.Context(), &routernative.Config{Rule: []*routernative.RoutingRule{{InboundTag: []string{"in"}, RuleTag: "webhook", TargetTag: &routernative.RoutingRule_Tag{Tag: "out"}, Webhook: &routernative.WebhookConfig{Url: webhook.URL}}}}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := router.Close(); err != nil {
			t.Error(err)
		}
	})
	finished := make(chan struct{})
	address, counts := routingAPI(t, observingRuleRemovalServer{RoutingServiceServer: native.NewRoutingServer(router, nil), finished: finished})
	client := apiClient(t, address, 3*time.Second, 4096)
	if _, err := client.TestRoute(t.Context(), routingdomain.RoutingProbe{Context: routingdomain.RoutingContext{InboundTag: "in"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("native webhook not running")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	var removalError error
	go func() { defer close(done); _, removalError = client.RemoveRoutingRule(ctx, "webhook") }()
	defer func() { cancel(); _ = client.Close(); <-done }()
	// Test-only observation of the published rules while native cleanup is blocked.
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for len(router.ListRule()) != 0 {
		select {
		case <-deadline.C:
			t.Fatal("native removal not applied")
		case <-ticker.C:
		}
	}
	select {
	case <-finished:
		t.Fatal("native removal skipped active webhook cleanup")
	default:
	}
	cancel()
	select {
	case <-done:
		err := removalError
		if !errors.Is(err, routingdomain.ErrRoutingMutationUnknown) || !errors.Is(err, context.Canceled) {
			t.Fatalf("post-effect cancellation claimed no effects: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled caller not released")
	}
	finish()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("native webhook cleanup not joined")
	}
	if counts.RPCs.Load() != 2 || len(router.ListRule()) != 0 {
		t.Fatal("removal retried or state restored after cancellation")
	}
}
