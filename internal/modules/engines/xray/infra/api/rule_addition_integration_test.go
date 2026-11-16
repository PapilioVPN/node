//go:build integration

package api_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	router "github.com/xtls/xray-core/app/router"
	native "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

type ruleAdditionServer struct {
	native.UnimplementedRoutingServiceServer
	add func(context.Context, *native.AddRuleRequest) (*native.AddRuleResponse, error)
}

func (s ruleAdditionServer) AddRule(ctx context.Context, r *native.AddRuleRequest) (*native.AddRuleResponse, error) {
	return s.add(ctx, r)
}

func TestRuleAdditionDispatchesCanonicalConfigAndExplicitModeOnce(t *testing.T) {
	config := &router.Config{Rule: []*router.RoutingRule{{RuleTag: " rule ", InboundTag: []string{"in"}, TargetTag: &router.RoutingRule_Tag{Tag: "out"}}}}
	content, err := proto.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	address, counts := routingAPI(t, ruleAdditionServer{add: func(_ context.Context, r *native.AddRuleRequest) (*native.AddRuleResponse, error) {
		decoded, err := r.Config.GetInstance()
		if err != nil || !proto.Equal(decoded, config) || !r.ShouldAppend {
			t.Errorf("canonical rule or explicit append changed: %v %v", r, err)
		}
		return &native.AddRuleResponse{}, nil
	}})
	ack, err := apiClient(t, address, time.Second, 4096).ApplyRoutingRule(t.Context(), domain.PreparedRoutingRule{Config: content, RuleTag: " rule "}, domain.AppendRoutingRule)
	if err != nil || ack.AcknowledgedAt.IsZero() || counts.rpcs.Load() != 1 {
		t.Fatalf("prepared rule was not dispatched exactly once: %+v %v", ack, err)
	}
}

func preparedRoutingRule(t *testing.T, tag string) domain.PreparedRoutingRule {
	t.Helper()
	config := &router.Config{Rule: []*router.RoutingRule{{RuleTag: tag, InboundTag: []string{"in"}, TargetTag: &router.RoutingRule_Tag{Tag: "new-out"}}}}
	content, err := proto.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return domain.PreparedRoutingRule{Config: content, RuleTag: tag}
}

func TestRuleAdditionNativeAppendAndReplaceAtEveryExistingRuleCount(t *testing.T) {
	for _, size := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			config := &router.Config{}
			for i := 0; i < size; i++ {
				config.Rule = append(config.Rule, &router.RoutingRule{RuleTag: fmt.Sprint("old-", size-i), InboundTag: []string{"in"}, TargetTag: &router.RoutingRule_Tag{Tag: fmt.Sprint("out-", i)}})
			}
			engine := new(router.Router)
			if err := engine.Init(t.Context(), config, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := engine.Close(); err != nil {
					t.Error(err)
				}
			})
			address, counts := routingAPI(t, native.NewRoutingServer(engine, nil))
			client := apiClient(t, address, time.Second, 4096)
			if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
				t.Fatal(err)
			}
			prepared := preparedRoutingRule(t, " exact>>>rule ")
			for i, mode := range []domain.RoutingRuleMode{domain.AppendRoutingRule, domain.ReplaceRoutingRules} {
				before := time.Now()
				ack, err := client.ApplyRoutingRule(t.Context(), prepared, mode)
				if err != nil || ack.AcknowledgedAt.Before(before) || ack.AcknowledgedAt.After(time.Now()) || counts.rpcs.Load() != int64(i+2) || counts.opened.Load() != 1 {
					t.Fatalf("native addition changed/replayed/scanned: %+v %v", ack, err)
				}
				rules := engine.ListRule()
				want := 1
				if mode == domain.AppendRoutingRule {
					want += size
				}
				if len(rules) != want || rules[len(rules)-1].GetRuleTag() != " exact>>>rule " || rules[len(rules)-1].GetOutboundTag() != "new-out" {
					t.Fatal("native append/replace semantics changed")
				}
				if mode == domain.AppendRoutingRule {
					for index, rule := range rules[:size] {
						if rule.GetRuleTag() != fmt.Sprint("old-", size-index) || rule.GetOutboundTag() != fmt.Sprint("out-", index) {
							t.Fatal("append reordered prior rules")
						}
					}
				}
			}
		})
	}
}

func TestRuleAdditionNativeFailuresAndOversizedAcknowledgementRetainEffects(t *testing.T) {
	for _, code := range []codes.Code{codes.Unimplemented, codes.NotFound, codes.InvalidArgument, codes.Unknown, codes.Unavailable, codes.ResourceExhausted, codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			address, counts := routingAPI(t, ruleAdditionServer{add: func(context.Context, *native.AddRuleRequest) (*native.AddRuleResponse, error) {
				return nil, status.Error(code, "private diagnostics")
			}})
			ack, err := apiClient(t, address, time.Second, 4096).ApplyRoutingRule(t.Context(), preparedRoutingRule(t, "rule"), domain.ReplaceRoutingRules)
			if err == nil || status.Code(err) != code || !ack.AcknowledgedAt.IsZero() || counts.rpcs.Load() != 1 || errors.Is(err, domain.ErrRoutingMutationUnknown) != (code != codes.Unimplemented) {
				t.Fatalf("application failure lost/replayed effects: %+v %v", ack, err)
			}
			if code == codes.Unimplemented && !errors.Is(err, domain.ErrRoutingManagementUnsupported) {
				t.Fatal(err)
			}
		})
	}
	response := &native.AddRuleResponse{}
	response.ProtoReflect().SetUnknown(protowire.AppendBytes(protowire.AppendTag(nil, 99, protowire.BytesType), []byte("future oversized metadata")))
	var calls atomic.Int32
	address, counts := routingAPI(t, ruleAdditionServer{add: func(context.Context, *native.AddRuleRequest) (*native.AddRuleResponse, error) {
		calls.Add(1)
		return response, nil
	}})
	ack, err := apiClient(t, address, time.Second, 1).ApplyRoutingRule(t.Context(), preparedRoutingRule(t, "rule"), domain.AppendRoutingRule)
	if !ack.AcknowledgedAt.IsZero() || !errors.Is(err, domain.ErrRoutingMutationUnknown) || !errors.Is(err, domain.ErrAPIResourceLimit) || counts.rpcs.Load() != 1 || calls.Load() != 1 {
		t.Fatalf("oversized acknowledgement became no effects/replayed: %+v %v", ack, err)
	}
}

func TestRuleAdditionSharesAdmissionAndJoinsAfterCancelCloseOrDeadline(t *testing.T) {
	for _, mode := range []string{"cancel", "close", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			address, counts := routingAPI(t, ruleAdditionServer{add: func(ctx context.Context, _ *native.AddRuleRequest) (*native.AddRuleResponse, error) {
				close(entered)
				<-ctx.Done()
				return nil, status.FromContextError(ctx.Err()).Err()
			}})
			client := apiClient(t, address, time.Second, 4096)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			var applicationError error
			prepared := preparedRoutingRule(t, "rule")
			go func() {
				defer close(done)
				_, applicationError = client.ApplyRoutingRule(ctx, prepared, domain.ReplaceRoutingRules)
			}()
			defer func() { cancel(); _ = client.Close(); <-done }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("native addition not started")
			}
			if _, err := client.ApplyRoutingRule(t.Context(), prepared, domain.AppendRoutingRule); !errors.Is(err, domain.ErrAPIBusy) || errors.Is(err, domain.ErrRoutingMutationUnknown) {
				t.Fatal("saturated addition was not rejected before effects")
			}
			if _, err := client.RemoveRoutingRule(t.Context(), "old"); !errors.Is(err, domain.ErrAPIBusy) {
				t.Fatal("removal bypassed shared capacity")
			}
			if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, domain.ErrAPIBusy) {
				t.Fatal("read bypassed shared capacity")
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
				if !errors.Is(applicationError, domain.ErrRoutingMutationUnknown) || counts.rpcs.Load() != 1 {
					t.Fatalf("lost application acknowledgement became no effects/retried: %v", applicationError)
				}
				if mode == "cancel" && !errors.Is(applicationError, context.Canceled) {
					t.Fatal(applicationError)
				}
				if mode == "deadline" && !errors.Is(applicationError, context.DeadlineExceeded) {
					t.Fatal(applicationError)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("application caller not joined")
			}
		})
	}
}

type observingRuleAdditionServer struct {
	native.RoutingServiceServer
	finished chan struct{}
}

func (s observingRuleAdditionServer) AddRule(ctx context.Context, r *native.AddRuleRequest) (*native.AddRuleResponse, error) {
	defer close(s.finished)
	return s.RoutingServiceServer.AddRule(ctx, r)
}

func TestRuleAdditionReplacementCanApplyBeforeOldWebhookCleanupCompletes(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { close(entered); <-release; w.WriteHeader(204) }))
	t.Cleanup(webhook.Close)
	engine := new(router.Router)
	if err := engine.Init(t.Context(), &router.Config{Rule: []*router.RoutingRule{{RuleTag: "old", InboundTag: []string{"in"}, TargetTag: &router.RoutingRule_Tag{Tag: "old-out"}, Webhook: &router.WebhookConfig{Url: webhook.URL}}}}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Error(err)
		}
	})
	finished := make(chan struct{})
	address, counts := routingAPI(t, observingRuleAdditionServer{RoutingServiceServer: native.NewRoutingServer(engine, nil), finished: finished})
	client := apiClient(t, address, 3*time.Second, 4096)
	if _, err := client.TestRoute(t.Context(), domain.RoutingProbe{Context: domain.RoutingContext{InboundTag: "in"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("native old webhook did not start")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	var applicationError error
	prepared := preparedRoutingRule(t, "new")
	go func() {
		defer close(done)
		_, applicationError = client.ApplyRoutingRule(ctx, prepared, domain.ReplaceRoutingRules)
	}()
	defer func() { cancel(); _ = client.Close(); <-done }()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		rules := engine.ListRule()
		if len(rules) == 1 && rules[0].GetRuleTag() == "new" {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("replacement not published before cleanup")
		}
	}
	select {
	case <-finished:
		t.Fatal("replacement skipped old webhook cleanup")
	default:
	}
	cancel()
	select {
	case <-done:
		if !errors.Is(applicationError, domain.ErrRoutingMutationUnknown) || !errors.Is(applicationError, context.Canceled) {
			t.Fatalf("post-publication cancellation lost effects: %v", applicationError)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled native caller not joined")
	}
	finish()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("native replacement cleanup not joined")
	}
	if counts.rpcs.Load() != 2 {
		t.Fatal("replacement retried")
	}
}
