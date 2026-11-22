package services_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
)

func TestInvalidProbeIsRejectedBeforeNativeWork(t *testing.T) {
	for name, request := range map[string]domain.ProbeRequest{
		"identity":       {InstanceID: "invalid.id"},
		"network":        {InstanceID: "one", Context: domain.Context{Network: 1}},
		"address":        {InstanceID: "one", Context: domain.Context{SourceIPs: []netip.Addr{{}}}},
		"zone":           {InstanceID: "one", Context: domain.Context{TargetIPs: []netip.Addr{netip.MustParseAddr("fe80::1%eth0")}}},
		"selector count": {InstanceID: "one", FieldSelectors: make([]string, 1025)},
		"selector text":  {InstanceID: "one", FieldSelectors: []string{"\xff"}},
		"domain bound":   {InstanceID: "one", Context: domain.Context{TargetDomain: strings.Repeat("x", 4097)}},
	} {
		t.Run(name, func(t *testing.T) {
			b := &backend{}
			s, _ := setup(b, nil)
			result, err := s.Probe(context.Background(), request)
			if !errors.Is(err, domain.ErrInvalidArgument) || result.Operation.Effect != domain.NotApplied || result.Route != nil || b.writes != 0 {
				t.Fatalf("result=%+v error=%v native calls=%d", result, err, b.writes)
			}
		})
	}
}

func TestInvalidRuleJSONDoesNotCompileOrDispatch(t *testing.T) {
	for _, data := range []string{"{", "null", "[]", "true", `"text"`, "{} trailing", strings.Repeat(" ", domain.MaxRuleBytes+1)} {
		b := &backend{}
		preparations := 0
		s, _ := setup(b, prepareFunc(func(context.Context, string, []byte) ([]byte, error) {
			preparations++
			return []byte("compiled"), nil
		}))
		result, err := s.Add(context.Background(), domain.AddRequest{InstanceID: "one", Mode: domain.Append, RuleJSON: []byte(data)})
		if !errors.Is(err, domain.ErrInvalidArgument) || result.Effect != domain.NotApplied || preparations != 0 || b.writes != 0 {
			t.Fatalf("input bytes=%d result=%+v error=%v preparations=%d native calls=%d", len(data), result, err, preparations, b.writes)
		}
	}
}

func TestInvalidRoutingTagsAreRejectedByService(t *testing.T) {
	ctx := context.Background()
	for _, tag := range []string{"", "\xff", strings.Repeat("x", 4097)} {
		b := &backend{}
		s, _ := setup(b, nil)
		removed, removeErr := s.Remove(ctx, "one", tag)
		empty := ""
		overridden, overrideErr := s.Override(ctx, domain.OverrideRequest{InstanceID: "one", BalancerTag: tag, Target: &empty})
		_, balancerErr := s.Balancer(ctx, "one", tag)
		if !errors.Is(removeErr, domain.ErrInvalidArgument) || !errors.Is(overrideErr, domain.ErrInvalidArgument) || !errors.Is(balancerErr, domain.ErrInvalidArgument) || removed.Effect != domain.NotApplied || overridden.Effect != domain.NotApplied || b.writes != 0 {
			t.Fatalf("remove=%+v/%v override=%+v/%v balancer=%v native writes=%d", removed, removeErr, overridden, overrideErr, balancerErr, b.writes)
		}
	}
	b := &backend{}
	s, _ := setup(b, nil)
	if _, err := s.Status(ctx, "invalid.id"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatal("invalid status identity", err)
	}
	if _, err := s.Balancer(ctx, "invalid.id", "balancer"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatal("invalid balancer identity", err)
	}
}
