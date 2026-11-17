package services_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type deltaApplier func(context.Context, domain.InboundUserDelta) (domain.UserDeltaReport, error)

func (f deltaApplier) ApplyInboundUserDelta(ctx context.Context, input domain.InboundUserDelta) (domain.UserDeltaReport, error) {
	return f(ctx, input)
}

func TestUserDeltaBoundsBothCollectionsBeforeOneNativeInvocation(t *testing.T) {
	calls := 0
	port := deltaApplier(func(context.Context, domain.InboundUserDelta) (domain.UserDeltaReport, error) {
		calls++
		return domain.UserDeltaReport{}, nil
	})
	service := services.NewUserDeltaService(map[instances.InstanceID]services.UserDeltaBinding{" edge ": {Applier: port, MaxUsers: 1}, "unconfigured": {Applier: port}, "disabled": {MaxUsers: 1}})
	for id, cause := range map[instances.InstanceID]error{"missing": instances.ErrNotFound, "edge": instances.ErrNotFound, "unconfigured": domain.ErrUserCollectionsDisabled, "disabled": domain.ErrAPIDisabled, " edge ": domain.ErrUserCollectionTooLarge} {
		result, err := service.ApplyInboundUserDelta(t.Context(), id, domain.InboundUserDelta{InboundTag: " exact ", Users: make([]domain.InboundUser, 1), RemoveEmails: []string{"removed"}})
		if !errors.Is(err, cause) || calls != 0 || result.InstanceID != id || result.InboundTag != " exact " {
			t.Fatalf("invalid delta selection/budget reached native effects: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ApplyInboundUserDelta(ctx, "missing", domain.InboundUserDelta{}); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("canceled delta reached lookup or native work")
	}
}

func TestUserDeltaDelegatesWholePlansAndRetainsPartialOrLateCompletion(t *testing.T) {
	for _, count := range []int{0, 1, 100, 1000} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/fail=%v", count, fail), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				input := domain.InboundUserDelta{InboundTag: " exact ", Users: make([]domain.InboundUser, count), RemoveEmails: make([]string, count)}
				report := domain.UserDeltaReport{ObservedAt: time.UnixMilli(0), Steps: make([]domain.UserMutationStep, count)}
				calls := 0
				var cause error
				if fail {
					cause = errors.Join(domain.ErrUserMutationUnknown, context.DeadlineExceeded)
				}
				bindings := map[instances.InstanceID]services.UserDeltaBinding{" edge ": {MaxUsers: 2000, Applier: deltaApplier(func(received context.Context, got domain.InboundUserDelta) (domain.UserDeltaReport, error) {
					calls++
					if received != ctx || got.InboundTag != input.InboundTag || len(got.Users) != count || len(got.RemoveEmails) != count || count > 0 && (&got.Users[0] != &input.Users[0] || &got.RemoveEmails[0] != &input.RemoveEmails[0]) {
						t.Fatal("delta inputs were changed, copied or split")
					}
					if !fail {
						cancel()
					}
					return report, cause
				})}}
				service := services.NewUserDeltaService(bindings)
				delete(bindings, " edge ")
				result, err := service.ApplyInboundUserDelta(ctx, " edge ", input)
				if err != cause || calls != 1 || result.InstanceID != " edge " || result.InboundTag != input.InboundTag || result.Report.ObservedAt != report.ObservedAt || len(result.Report.Steps) != count || count > 0 && &result.Report.Steps[0] != &report.Steps[0] {
					t.Fatal("delta lost owned results, partial effects or accepted late completion")
				}
			})
		}
	}
}
