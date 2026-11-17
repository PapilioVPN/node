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

type usersAdder func(context.Context, domain.InboundUserAdditions) (domain.UserAdditionsReport, error)

func (f usersAdder) AddInboundUsers(ctx context.Context, input domain.InboundUserAdditions) (domain.UserAdditionsReport, error) {
	return f(ctx, input)
}

func TestUserAdditionsRespectConfiguredLimitsBeforeNativeEffects(t *testing.T) {
	calls := 0
	adder := usersAdder(func(context.Context, domain.InboundUserAdditions) (domain.UserAdditionsReport, error) {
		calls++
		return domain.UserAdditionsReport{}, nil
	})
	service := services.NewUserAdditionsService(map[instances.InstanceID]services.UserAdditionsBinding{
		" edge ": {Adder: adder, MaxUsers: 1}, "unconfigured": {Adder: adder}, "disabled": {MaxUsers: 1},
	})
	for id, cause := range map[instances.InstanceID]error{"missing": instances.ErrNotFound, "edge": instances.ErrNotFound, "unconfigured": domain.ErrUserCollectionsDisabled, "disabled": domain.ErrAPIDisabled, " edge ": domain.ErrUserCollectionTooLarge} {
		if _, err := service.AddInboundUsers(t.Context(), id, domain.InboundUserAdditions{Users: make([]domain.InboundUser, 2)}); !errors.Is(err, cause) || calls != 0 {
			t.Fatalf("invalid selection/bound reached native work: id=%q err=%v calls=%d", id, err, calls)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.AddInboundUsers(ctx, " edge ", domain.InboundUserAdditions{}); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("canceled collection reached native work")
	}
}

func TestUserAdditionsUseOneCollectionPortAndRetainPartialResults(t *testing.T) {
	for _, count := range []int{0, 1, 100, 1000} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/fail=%v", count, fail), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				input := domain.InboundUserAdditions{InboundTag: " exact>>>tag ", Users: make([]domain.InboundUser, count)}
				report := domain.UserAdditionsReport{Items: make([]domain.UserAdditionResult, count)}
				for i := range input.Users {
					input.Users[i].Email = fmt.Sprint(" user-", i, " ")
					report.Items[i] = domain.UserAdditionResult{Email: input.Users[i].Email, State: domain.UserMutationAcknowledged, AcknowledgedAt: time.UnixMilli(1700000000123)}
				}
				var cause error
				if fail {
					cause = errors.Join(domain.ErrUserMutationUnknown, context.DeadlineExceeded)
					if count > 0 {
						report.Items[count-1] = domain.UserAdditionResult{Email: input.Users[count-1].Email, State: domain.UserMutationUnknown, Failure: cause}
					}
				}
				calls := 0
				bindings := map[instances.InstanceID]services.UserAdditionsBinding{" edge ": {MaxUsers: 1000, Adder: usersAdder(func(received context.Context, got domain.InboundUserAdditions) (domain.UserAdditionsReport, error) {
					calls++
					if received != ctx || got.InboundTag != input.InboundTag || len(got.Users) != count || count > 0 && &got.Users[0] != &input.Users[0] {
						t.Fatal("collection inputs/context were changed or copied")
					}
					if !fail {
						cancel() // Preserve a valid final native acknowledgement.
					}
					return report, cause
				})}}
				service := services.NewUserAdditionsService(bindings)
				delete(bindings, " edge ")
				result, err := service.AddInboundUsers(ctx, " edge ", input)
				if err != cause || calls != 1 || result.InstanceID != " edge " || result.InboundTag != input.InboundTag || len(result.Report.Items) != count || count > 0 && &result.Report.Items[0] != &report.Items[0] {
					t.Fatal("collection was replayed, lost progress or discarded a late acknowledgement")
				}
			})
		}
	}
}
