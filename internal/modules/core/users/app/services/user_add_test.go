package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/users/app/services"
	"github.com/PapilioVPN/node/internal/modules/core/users/domain"
)

type userAdder func(context.Context, domain.InboundUserAddition) (domain.UserMutationAcknowledgement, error)

func (f userAdder) AddInboundUser(ctx context.Context, addition domain.InboundUserAddition) (domain.UserMutationAcknowledgement, error) {
	return f(ctx, addition)
}

func TestUserAdditionSelectsOneInstanceWithoutRemovalOrReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	account := &domain.VLESSAccount{ID: "8e781ad6-dae6-4ed4-9d7b-8c247f735869", Flow: "xtls-rprx-vision"}
	input := domain.InboundUserAddition{InboundTag: " exact>>>tag ", User: domain.InboundUser{Email: " Mixed@Example ", Level: 42, Account: domain.UserAccount{Protocol: domain.VLESS, VLESS: account}}}
	ack := domain.UserMutationAcknowledgement{AcknowledgedAt: time.UnixMilli(1700000000123)}
	calls := 0
	bindings := map[instances.InstanceID]services.InboundUserAdder{" edge ": userAdder(func(received context.Context, addition domain.InboundUserAddition) (domain.UserMutationAcknowledgement, error) {
		calls++
		if received != ctx || addition.InboundTag != input.InboundTag || addition.User.Email != input.User.Email || addition.User.Level != 42 || addition.User.Account.VLESS != account {
			t.Fatal("input/context changed")
		}
		cancel()
		return ack, nil
	}), "disabled": nil}
	service := services.NewUserAdditionService(bindings)
	delete(bindings, " edge ")
	result, err := service.AddInboundUser(ctx, " edge ", input)
	if err != nil || calls != 1 || result.InstanceID != " edge " || result.Selection != (domain.InboundUserSelection{InboundTag: input.InboundTag, Email: input.User.Email}) || result.Acknowledgement != ack {
		t.Fatalf("acknowledgement lost: %+v %v", result, err)
	}
	if _, err := service.AddInboundUser(ctx, " edge ", input); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("pre-canceled addition reached native")
	}
	for id, cause := range map[instances.InstanceID]error{"edge": instances.ErrNotFound, "missing": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		if _, err := service.AddInboundUser(t.Context(), id, input); !errors.Is(err, cause) {
			t.Fatalf("selection %q: %v", id, err)
		}
	}
}

func TestUserAdditionKeepsUnknownEffectWithoutPartialAcknowledgement(t *testing.T) {
	cause := errors.Join(domain.ErrUserMutationUnknown, context.DeadlineExceeded)
	calls := 0
	service := services.NewUserAdditionService(map[instances.InstanceID]services.InboundUserAdder{"edge": userAdder(func(context.Context, domain.InboundUserAddition) (domain.UserMutationAcknowledgement, error) {
		calls++
		return domain.UserMutationAcknowledgement{AcknowledgedAt: time.Now()}, cause
	})})
	result, err := service.AddInboundUser(t.Context(), "edge", domain.InboundUserAddition{})
	if result != (domain.InstanceInboundUserMutation{}) || !errors.Is(err, domain.ErrUserMutationUnknown) || !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("failed addition became success/replayed: %+v %v", result, err)
	}
}
