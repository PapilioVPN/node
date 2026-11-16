//go:build integration

package api_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/router/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	routingdomain "github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
	"github.com/PapilioVPN/node/internal/shared/engineapi"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

// Controlled native services verify SDK dispatch/bounds, not real Xray routing.
type routingServer struct {
	native.UnimplementedRoutingServiceServer
	list     func(context.Context) (*native.ListRuleResponse, error)
	balancer func(context.Context, string) (*native.GetBalancerInfoResponse, error)
}

func (s routingServer) ListRule(ctx context.Context, _ *native.ListRuleRequest) (*native.ListRuleResponse, error) {
	return s.list(ctx)
}
func (s routingServer) GetBalancerInfo(ctx context.Context, r *native.GetBalancerInfoRequest) (*native.GetBalancerInfoResponse, error) {
	return s.balancer(ctx, r.Tag)
}

func routingAPI(t *testing.T, service native.RoutingServiceServer) (string, *testutil.XrayAPICounts) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	counts := &testutil.XrayAPICounts{}
	server := grpc.NewServer(grpc.StatsHandler(counts))
	native.RegisterRoutingServiceServer(server, service)
	stats.RegisterStatsServiceServer(server, testutil.XrayRuntimeServer{Read: func(context.Context) (*stats.SysStatsResponse, error) { return &stats.SysStatsResponse{}, nil }})
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); server.GracefulStop(); <-done })
	return listener.Addr().String(), counts
}

func TestRoutingListHasOneCallAtEverySizeAndKeepsNativePriority(t *testing.T) {
	for _, count := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			items := make([]*native.ListRuleItem, count)
			for i := range items {
				items[i] = &native.ListRuleItem{Tag: fmt.Sprint("out-", count-i), RuleTag: fmt.Sprint("rule-", i%10)}
			}
			if count > 0 {
				items[0] = &native.ListRuleItem{}
			}
			address, counts := routingAPI(t, routingServer{list: func(context.Context) (*native.ListRuleResponse, error) {
				return &native.ListRuleResponse{Rules: items}, nil
			}})
			client := apiClient(t, address, time.Second, 128*1024)
			if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
				t.Fatal(err)
			}
			before, calls := time.Now(), counts.RPCs.Load()
			result, err := client.ReadRoutingRules(t.Context())
			if err != nil || len(result.Items) != count || result.ObservedAt.Before(before) || result.ObservedAt.After(time.Now()) || counts.RPCs.Load() != calls+1 || counts.Opened.Load() != 1 {
				t.Fatalf("routing list added calls/channels/truncated: %v", err)
			}
			for i, item := range result.Items {
				if item.RuleTag != items[i].RuleTag || item.OutboundTag != items[i].Tag {
					t.Fatal("routing order/opaque tags/duplicates changed")
				}
			}
		})
	}
}

func TestBalancerPreservesNativePresenceOrderAndOwnsItsReply(t *testing.T) {
	for _, override := range []*native.OverrideInfo{nil, {}, {Target: " exact>>>target "}} {
		for _, principal := range []*native.PrincipleTargetInfo{nil, {}, {Tag: []string{"z", "a", "a", ""}}} {
			address, counts := routingAPI(t, routingServer{balancer: func(_ context.Context, tag string) (*native.GetBalancerInfoResponse, error) {
				if tag != " exact>>>pool " {
					return nil, status.Error(codes.InvalidArgument, "tag changed")
				}
				return &native.GetBalancerInfoResponse{Balancer: &native.BalancerMsg{Override: override, PrincipleTarget: principal}}, nil
			}})
			client := apiClient(t, address, time.Second, 4096)
			result, err := client.ReadBalancerInfo(t.Context(), " exact>>>pool ")
			if err != nil || result.ObservedAt.IsZero() || (result.OverrideTarget == nil) != (override == nil) || (result.PrincipalTargets == nil) != (principal == nil) || counts.RPCs.Load() != 1 {
				t.Fatal("native presence/dispatch was lost")
			}
			if override != nil && *result.OverrideTarget != override.Target {
				t.Fatal("reported empty override changed")
			}
			if principal != nil && !reflect.DeepEqual(result.PrincipalTargets.Tags, principal.Tag) {
				t.Fatal("principal target order/duplicates changed")
			}
			if principal != nil && len(principal.Tag) > 0 {
				result.PrincipalTargets.Tags[0] = "caller-owned"
				again, err := client.ReadBalancerInfo(t.Context(), " exact>>>pool ")
				if err != nil || again.PrincipalTargets.Tags[0] != "z" {
					t.Fatal("returned tags aliased a reused native reply/cache")
				}
			}
		}
	}
}

func TestRoutingFailuresNeverReturnEmptySuccessOrRetry(t *testing.T) {
	for _, code := range []codes.Code{codes.NotFound, codes.Unimplemented, codes.Unavailable, codes.ResourceExhausted, codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			address, counts := routingAPI(t, routingServer{
				list: func(context.Context) (*native.ListRuleResponse, error) {
					return nil, status.Error(code, "private diagnostic")
				},
				balancer: func(context.Context, string) (*native.GetBalancerInfoResponse, error) {
					return nil, status.Error(code, "private diagnostic")
				},
			})
			client := apiClient(t, address, time.Second, 4096)
			rules, err := client.ReadRoutingRules(t.Context())
			if status.Code(err) != code || len(rules.Items) != 0 || !rules.ObservedAt.IsZero() || counts.RPCs.Load() != 1 {
				t.Fatal("native list failure became success or replayed")
			}
			info, err := client.ReadBalancerInfo(t.Context(), "pool")
			if status.Code(err) != code || info != (routingdomain.BalancerInfo{}) || counts.RPCs.Load() != 2 {
				t.Fatal("native balancer failure became success or replayed")
			}
		})
	}
	address, _ := routingAPI(t, routingServer{balancer: func(context.Context, string) (*native.GetBalancerInfoResponse, error) {
		return &native.GetBalancerInfoResponse{}, nil
	}})
	if info, err := apiClient(t, address, time.Second, 4096).ReadBalancerInfo(t.Context(), "pool"); !errors.Is(err, routingdomain.ErrInvalidRoutingResponse) || info != (routingdomain.BalancerInfo{}) {
		t.Fatal("missing balancer message became empty successful info")
	}
}

func TestRoutingReceiveLimitsAndSharedAdmissionObserveCancellation(t *testing.T) {
	address, _ := routingAPI(t, routingServer{
		list: func(context.Context) (*native.ListRuleResponse, error) {
			return &native.ListRuleResponse{Rules: []*native.ListRuleItem{{Tag: "large", RuleTag: "large"}}}, nil
		},
		balancer: func(context.Context, string) (*native.GetBalancerInfoResponse, error) {
			return &native.GetBalancerInfoResponse{Balancer: &native.BalancerMsg{Override: &native.OverrideInfo{Target: "large"}}}, nil
		},
	})
	limited := apiClient(t, address, time.Second, 1)
	if result, err := limited.ReadRoutingRules(t.Context()); !errors.Is(err, engineapi.ErrResourceLimit) || len(result.Items) != 0 {
		t.Fatal("oversized routing list became partial facts")
	}
	if result, err := limited.ReadBalancerInfo(t.Context(), "pool"); !errors.Is(err, engineapi.ErrResourceLimit) || result != (routingdomain.BalancerInfo{}) {
		t.Fatal("oversized balancer reply became successful facts")
	}
	entered := make(chan struct{})
	address, _ = routingAPI(t, routingServer{list: func(ctx context.Context) (*native.ListRuleResponse, error) {
		close(entered)
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}})
	client := apiClient(t, address, time.Second, 4096)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := client.ReadRoutingRules(ctx); done <- err }()
	defer func() { cancel(); _ = client.Close() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("native routing read did not start")
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, engineapi.ErrBusy) {
		t.Fatal("stats bypassed routing capacity")
	}
	if _, err := client.ReadBalancerInfo(t.Context(), "pool"); !errors.Is(err, engineapi.ErrBusy) {
		t.Fatal("balancer bypassed routing capacity")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("routing cancellation lost")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("routing read failed to join")
	}
}
