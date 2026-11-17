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

type userObservationReader struct {
	one  func(context.Context, domain.InboundUserSelection) (domain.InboundUserObservation, error)
	list func(context.Context, string) (domain.InboundUsersObservation, error)
}

func (r userObservationReader) ReadInboundUser(ctx context.Context, s domain.InboundUserSelection) (domain.InboundUserObservation, error) {
	return r.one(ctx, s)
}
func (r userObservationReader) ReadInboundUsers(ctx context.Context, tag string) (domain.InboundUsersObservation, error) {
	return r.list(ctx, tag)
}

func TestUserObservationSelectsOneNativeLookupAndPreservesAbsenceAndStoredIdentity(t *testing.T) {
	ctx := t.Context()
	selection := domain.InboundUserSelection{InboundTag: " exact>>>tag ", Email: "Mixed@Example"}
	stored := &domain.ObservedInboundUser{Email: "mixed@example", Level: 42, Account: domain.ObservedUserAccount{TypeName: "native.account", Protobuf: []byte{7}}}
	observed := time.UnixMilli(1700000000123)
	calls := 0
	bindings := map[instances.InstanceID]services.UserObservationReader{" edge ": userObservationReader{one: func(received context.Context, s domain.InboundUserSelection) (domain.InboundUserObservation, error) {
		calls++
		if received != ctx || s != selection {
			t.Fatal("lookup selection/context changed")
		}
		return domain.InboundUserObservation{User: stored, ObservedAt: observed}, nil
	}}, "disabled": nil}
	service := services.NewUserObservationService(bindings)
	delete(bindings, " edge ")
	result, err := service.GetInboundUser(ctx, " edge ", selection)
	if err != nil || result.InstanceID != " edge " || result.Selection != selection || result.Observation.User != stored || result.Observation.ObservedAt != observed || calls != 1 {
		t.Fatalf("lookup result/context/selection changed: %v", err)
	}
	stored = nil
	result, err = service.GetInboundUser(ctx, " edge ", selection)
	if err != nil || result.Observation.User != nil || result.Observation.ObservedAt != observed || calls != 2 {
		t.Fatal("completed absence was lost")
	}
	for id, cause := range map[instances.InstanceID]error{"edge": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		if _, err := service.GetInboundUser(ctx, id, selection); !errors.Is(err, cause) || calls != 2 {
			t.Fatalf("lookup crossed configured instance selection: %v", err)
		}
	}
}

func TestUserListUsesOneCollectionReadAndPreservesAnonymousUsersAndDuplicates(t *testing.T) {
	ctx := t.Context()
	items := []domain.ObservedInboundUser{{Email: "z", Level: 2}, {Email: "a", Level: 1}, {Email: "a", Level: 1}, {Email: "", Level: 3}}
	items[1].Account = domain.ObservedUserAccount{TypeName: "account", Protobuf: []byte{1}}
	items[2].Account = domain.ObservedUserAccount{TypeName: "account", Protobuf: []byte{2}}
	observed := time.UnixMilli(1700000000123)
	calls := 0
	service := services.NewUserObservationService(map[instances.InstanceID]services.UserObservationReader{"edge": userObservationReader{list: func(received context.Context, tag string) (domain.InboundUsersObservation, error) {
		calls++
		if received != ctx || tag != " exact>>>tag " {
			t.Fatal("list selection/context changed")
		}
		return domain.InboundUsersObservation{Users: items, ObservedAt: observed}, nil
	}}})
	result, err := service.ListInboundUsers(ctx, "edge", " exact>>>tag ")
	if err != nil || calls != 1 || result.InstanceID != "edge" || result.InboundTag != " exact>>>tag " || result.Observation.ObservedAt != observed || len(result.Observation.Users) != 4 {
		t.Fatal("list selection/ownership/count changed")
	}
	if result.Observation.Users[0].Email != "" || result.Observation.Users[1].Account.Protobuf[0] != 1 || result.Observation.Users[2].Account.Protobuf[0] != 2 || result.Observation.Users[3].Email != "z" {
		t.Fatal("stable sorting dropped anonymous users/duplicates or compared credentials")
	}
}

func TestUserReadFailureOrLateCancellationReturnsNoPartialFacts(t *testing.T) {
	for _, cause := range []error{domain.ErrInvalidUserObservation, domain.ErrAPIResourceLimit, nil} {
		ctx, cancel := context.WithCancel(t.Context())
		reader := userObservationReader{
			one: func(context.Context, domain.InboundUserSelection) (domain.InboundUserObservation, error) {
				if cause == nil {
					cancel()
				}
				return domain.InboundUserObservation{User: &domain.ObservedInboundUser{Email: "partial"}, ObservedAt: time.Now()}, cause
			},
			list: func(context.Context, string) (domain.InboundUsersObservation, error) {
				if cause == nil {
					cancel()
				}
				return domain.InboundUsersObservation{Users: []domain.ObservedInboundUser{{Email: "partial"}}, ObservedAt: time.Now()}, cause
			},
		}
		service := services.NewUserObservationService(map[instances.InstanceID]services.UserObservationReader{"edge": reader})
		one, err := service.GetInboundUser(ctx, "edge", domain.InboundUserSelection{InboundTag: "in", Email: "user"})
		if one != (domain.InstanceInboundUserObservation{}) || err == nil || cause != nil && !errors.Is(err, cause) || cause == nil && !errors.Is(err, context.Canceled) {
			t.Fatal("failed lookup returned partial facts")
		}
		cancel()
		if _, err := service.GetInboundUser(ctx, "edge", domain.InboundUserSelection{}); !errors.Is(err, context.Canceled) {
			t.Fatal("pre-canceled lookup reached reader")
		}
		ctx, cancel = context.WithCancel(t.Context())
		list, err := service.ListInboundUsers(ctx, "edge", "in")
		if len(list.Observation.Users) != 0 || !list.Observation.ObservedAt.IsZero() || err == nil || cause != nil && !errors.Is(err, cause) || cause == nil && !errors.Is(err, context.Canceled) {
			t.Fatal("failed collection returned partial facts")
		}
		cancel()
		if _, err := service.ListInboundUsers(ctx, "edge", "in"); !errors.Is(err, context.Canceled) {
			t.Fatal("pre-canceled collection reached reader")
		}
	}
}
