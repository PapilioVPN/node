package api

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	native "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type connection struct {
	call   func(string, any, any) error
	calls  int
	noPeer bool
}

func (c *connection) Invoke(ctx context.Context, method string, input, output any, options ...grpc.CallOption) error {
	c.calls++
	if !c.noPeer {
		for _, o := range options {
			if p, ok := o.(grpc.PeerCallOption); ok {
				p.PeerAddr.Addr = &net.TCPAddr{Port: 1}
			}
		}
	}
	return c.call(method, input, output)
}
func (*connection) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("unexpected stream")
}
func TestProjectionUsesNativeReplyAndForwardsSelectors(t *testing.T) {
	c := &connection{call: func(method string, input, output any) error {
		request := input.(*native.TestRouteRequest)
		if !request.PublishResult || len(request.FieldSelectors) != 2 || request.FieldSelectors[0] != "out" || request.FieldSelectors[1] != "unknown" || request.RoutingContext.VlessRoute != 65535 || len(request.RoutingContext.SourceIPs) != 2 {
			t.Fatal(request)
		}
		proto.Merge(output.(proto.Message), &native.RoutingContext{OutboundTag: "selected"})
		return nil
	}}
	r := New(c)
	response, n, err := r.Probe(context.Background(), domain.ProbeRequest{InstanceID: "one", Context: domain.Context{VLESSRoute: 65535, SourceIPs: []netip.Addr{netip.MustParseAddr("::ffff:127.0.0.1"), netip.MustParseAddr("::ffff:127.0.0.1")}}, FieldSelectors: []string{"out", "unknown"}, PublishResult: true})
	if err != nil || !n.Acknowledged || response.OutboundTag != "selected" || response.VLESSRoute != 0 || len(response.SourceIPs) != 0 || c.calls != 1 {
		t.Fatal(response, n, err)
	}
}
func TestMutationErrorsNeverImplyRollbackOrReplay(t *testing.T) {
	for _, test := range []struct {
		cause  error
		noPeer bool
		effect domain.Effect
		fence  bool
	}{
		{nil, false, domain.Completed, false}, {status.Error(codes.Unimplemented, "private"), false, domain.NotApplied, false}, {status.Error(codes.Unknown, "private"), false, domain.Unknown, false}, {status.Error(codes.Unavailable, "lost ACK"), false, domain.Unknown, true}, {status.Error(codes.ResourceExhausted, "large reply"), false, domain.Unknown, true}, {status.Error(codes.Unavailable, "refused"), true, domain.NotApplied, false},
	} {
		c := &connection{noPeer: test.noPeer, call: func(string, any, any) error { return test.cause }}
		n, err := New(c).Override(context.Background(), "balancer", "")
		if n.Effect != test.effect || n.RequiresInstanceRestart != test.fence || c.calls != 1 || (test.cause != nil && !errors.Is(err, test.cause)) {
			t.Fatalf("%+v %v", n, err)
		}
	}
}
func TestMalformedProjectionIsUnknownAndInvalidInputIsUnsent(t *testing.T) {
	c := &connection{call: func(_ string, _ any, out any) error { out.(*native.RoutingContext).TargetPort = 65536; return nil }}
	r := New(c)
	_, n, err := r.Probe(context.Background(), domain.ProbeRequest{InstanceID: "one"})
	if !errors.Is(err, domain.ErrInvalidResponse) || n.Effect != domain.Unknown {
		t.Fatal(n, err)
	}
	_, n, err = r.Probe(context.Background(), domain.ProbeRequest{InstanceID: "one", Context: domain.Context{Network: 1}})
	if !errors.Is(err, domain.ErrInvalidArgument) || n.Effect != domain.NotApplied || c.calls != 1 {
		t.Fatal(n, err)
	}
}
func TestNativeRuleListOneCallAtAllSizes(t *testing.T) {
	for _, n := range []int{0, 1, 50, 51, 1000} {
		c := &connection{call: func(_ string, _ any, out any) error {
			response := out.(*native.ListRuleResponse)
			for range n {
				response.Rules = append(response.Rules, &native.ListRuleItem{Tag: "opaque", RuleTag: ""})
			}
			return nil
		}}
		rows, err := New(c).ListRules(context.Background())
		if err != nil || len(rows) != n || c.calls != 1 {
			t.Fatal(n, len(rows), err, c.calls)
		}
	}
}

func TestBalancerPresenceAndOversizedProjection(t *testing.T) {
	for _, present := range []bool{false, true} {
		c := &connection{call: func(_ string, _ any, out any) error {
			b := &native.BalancerMsg{}
			if present {
				b.Override = &native.OverrideInfo{}
				b.PrincipleTarget = &native.PrincipleTargetInfo{}
			}
			out.(*native.GetBalancerInfoResponse).Balancer = b
			return nil
		}}
		b, err := New(c).Balancer(context.Background(), "b")
		if err != nil || (b.Override != nil) != present || (b.PrincipalTargets != nil) != present || c.calls != 1 {
			t.Fatal(b, err)
		}
	}
	c := &connection{call: func(_ string, _ any, out any) error {
		out.(*native.GetBalancerInfoResponse).Balancer = &native.BalancerMsg{Override: &native.OverrideInfo{Target: "private"}, PrincipleTarget: &native.PrincipleTargetInfo{Tag: make([]string, 51)}}
		return nil
	}}
	b, err := New(c).Balancer(context.Background(), "b")
	if !errors.Is(err, domain.ErrCapacity) || b.Override != nil || b.PrincipalTargets != nil {
		t.Fatal("oversized response leaked partial facts", b, err)
	}
}
