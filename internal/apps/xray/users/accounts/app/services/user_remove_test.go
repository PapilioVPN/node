package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type userRemover func(context.Context, domain.InboundUserSelection) (domain.UserMutationAcknowledgement, error)

func (f userRemover) RemoveInboundUser(ctx context.Context, selection domain.InboundUserSelection) (domain.UserMutationAcknowledgement, error) {
	return f(ctx, selection)
}

func TestUserRemovalSelectsExactInstanceAndRetainsLateAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	selection := domain.InboundUserSelection{InboundTag: " exact>>>tag ", Email: " Mixed@Example "}
	ack := domain.UserMutationAcknowledgement{AcknowledgedAt: time.UnixMilli(1700000000123)}
	calls := 0
	bindings := map[instances.InstanceID]services.InboundUserRemover{" edge ": userRemover(func(received context.Context, selected domain.InboundUserSelection) (domain.UserMutationAcknowledgement, error) {
		calls++
		if received != ctx || selected != selection {
			t.Fatal("identity/context changed")
		}
		cancel()
		return ack, nil
	}), "other": userRemover(func(context.Context, domain.InboundUserSelection) (domain.UserMutationAcknowledgement, error) {
		t.Fatal("unselected instance was changed")
		return domain.UserMutationAcknowledgement{}, nil
	}), "disabled": nil}
	service := services.NewUserRemovalService(bindings)
	delete(bindings, " edge ")
	result, err := service.RemoveInboundUser(ctx, " edge ", selection)
	if err != nil || result.InstanceID != " edge " || result.Selection != selection || result.Acknowledgement != ack || calls != 1 {
		t.Fatalf("acknowledgement lost: %+v %v", result, err)
	}
	if _, err := service.RemoveInboundUser(ctx, " edge ", selection); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("pre-canceled removal reached native: %v calls=%d", err, calls)
	}
	for id, cause := range map[instances.InstanceID]error{"missing": instances.ErrNotFound, "edge": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		if _, err := service.RemoveInboundUser(t.Context(), id, selection); !errors.Is(err, cause) {
			t.Fatalf("selection %q: %v", id, err)
		}
	}
}

func TestUserRemovalRetainsUnknownCauseWithoutPartialAcknowledgementOrReplay(t *testing.T) {
	cause := errors.Join(domain.ErrUserMutationUnknown, context.DeadlineExceeded, errors.New("private native diagnostics"))
	calls := 0
	service := services.NewUserRemovalService(map[instances.InstanceID]services.InboundUserRemover{"edge": userRemover(func(context.Context, domain.InboundUserSelection) (domain.UserMutationAcknowledgement, error) {
		calls++
		return domain.UserMutationAcknowledgement{AcknowledgedAt: time.Now()}, cause
	})})
	result, err := service.RemoveInboundUser(t.Context(), "edge", domain.InboundUserSelection{InboundTag: "in", Email: "user"})
	if result != (domain.InstanceInboundUserMutation{}) || !errors.Is(err, domain.ErrUserMutationUnknown) || !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("lost outcome/cause or replayed: %+v %v calls=%d", result, err, calls)
	}
}
