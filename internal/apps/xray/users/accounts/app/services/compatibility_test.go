package services_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type profiles struct {
	profile       domain.Inbound
	gets, selects int
	err           error
}

func (p *profiles) Get(context.Context, string, string) (domain.Inbound, error) {
	p.gets++
	return p.profile, p.err
}
func (p *profiles) Select(_ context.Context, _ string, tags []string) ([]domain.Inbound, error) {
	p.selects++
	result := make([]domain.Inbound, len(tags))
	for i, tag := range tags {
		result[i] = p.profile
		result[i].Tag = tag
	}
	return result, p.err
}
func TestIncompatibleSingleAccountsNeverReachNativeMutation(t *testing.T) {
	for _, scenario := range []string{"mismatch", "unsupported", "missing", "ambiguous", "generation", "uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			p := &profiles{profile: domain.Inbound{Generation: 1, LiveAccounts: true, AccountType: "test.account"}}
			switch scenario {
			case "mismatch":
				p.profile.AccountType = "other.account"
			case "unsupported":
				p.profile.LiveAccounts = false
			case "missing":
				p.err = domain.ErrInboundMissing
			case "ambiguous":
				p.err = domain.ErrInboundAmbiguous
			case "generation":
				p.profile.Generation = 2
			case "uncertain":
				p.err = domain.ErrUncertain
			}
			c := client{add: func(context.Context, string, []byte) (domain.NativeResult, error) {
				t.Fatal("unsafe native mutation dispatched")
				return domain.NativeResult{}, nil
			}}
			s, err := services.New([]services.Binding{{ID: "a", Client: c}}, &execution{state: instances.Running, generation: 1}, p, accountInspector{})
			if err != nil {
				t.Fatal(err)
			}
			operation, err := s.Add(context.Background(), "a", "tag", []byte("credential"))
			if scenario == "uncertain" && !operation.RequiresInstanceRestart {
				t.Fatal("upstream execution fence was lost", operation)
			}
			if err == nil || operation.Effect != domain.NotApplied || operation.Guidance == nil || operation.Guidance.NextAction == "" || p.gets != 1 {
				t.Fatal(operation, err, p.gets)
			}
		})
	}
}
func TestBatchCompatibilityLookupDoesNotGrowWithAccounts(t *testing.T) {
	for _, count := range []int{1, 50, 51, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			p := &profiles{profile: domain.Inbound{Generation: 1, LiveAccounts: true, AccountType: "test.account"}}
			calls := 0
			c := client{add: func(context.Context, string, []byte) (domain.NativeResult, error) {
				calls++
				return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
			}}
			s, _ := services.New([]services.Binding{{ID: "a", Client: c}}, &execution{state: instances.Running, generation: 1}, p, accountInspector{})
			items := make([]domain.Addition, count)
			for i := range items {
				items[i] = domain.Addition{InboundTag: fmt.Sprint(i % 3), Configuration: []byte("credential")}
			}
			result, err := s.AddMany(context.Background(), "a", items, 1)
			if err != nil || result.Operation.Effect != domain.Completed || calls != count || p.selects != 1 || p.gets != 0 {
				t.Fatal(result.Operation, err, calls, p.selects, p.gets)
			}
			p.profile.AccountType = "foreign.account"
			result, err = s.AddMany(context.Background(), "a", items, 1)
			if !errors.Is(err, domain.ErrIncompatible) || result.Items[0].Attempted || result.Items[0].Operation.Guidance.ActualAccountType != "test.account" {
				t.Fatal(result, err)
			}
		})
	}
}
