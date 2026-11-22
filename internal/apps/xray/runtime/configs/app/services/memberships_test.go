package services

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

func TestMembershipUsesOneCompilationAndOneWholeTransformation(t *testing.T) {
	for _, count := range []int{1, 50, 51, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			plans, repo, run, compiler, _ := setupReview(t)
			transformations := 0
			transform := func(_ context.Context, content []byte, sets []accounts.InboundUserSet) ([]byte, error) {
				transformations++
				if string(content) != "old" || len(sets) != 1 || len(sets[0].Accounts) != count {
					t.Fatal("unexpected whole-source selection")
				}
				return []byte("restart"), nil
			}
			members, _ := NewMemberships(plans, transform, func([]domain.Source) bool { return true })
			plan, err := members.Plan(context.Background(), "a", 1, 1, []accounts.InboundUserSet{{InboundTag: "selected", Accounts: make([][]byte, count)}})
			if err != nil || plan.Mode != domain.Restart || compiler.calls != 1 || transformations != 1 || repo.writes != 0 || run.restarts != 0 {
				t.Fatal(plan, err, compiler.calls, transformations)
			}
		})
	}
}
func TestMembershipRejectsUnrepresentableSourceContextBeforeCompilation(t *testing.T) {
	plans, repo, run, compiler, _ := setupReview(t)
	members, _ := NewMemberships(plans, func(context.Context, []byte, []accounts.InboundUserSet) ([]byte, error) {
		t.Fatal("unrepresentable source was transformed")
		return nil, nil
	}, func([]domain.Source) bool { return false })
	_, err := members.Plan(context.Background(), "a", 1, 1, []accounts.InboundUserSet{{InboundTag: "selected"}})
	if !errors.Is(err, domain.ErrUnsupported) || compiler.calls != 0 || repo.writes != 0 || run.restarts != 0 {
		t.Fatal(err)
	}
}
