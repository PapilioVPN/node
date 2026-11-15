package services

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// UserObservationReader keeps single and collection operations distinct. Successful
// results transfer ownership; no count preflight, listing fallback or cache.
type UserObservationReader interface {
	ReadInboundUser(context.Context, domain.InboundUserSelection) (domain.InboundUserObservation, error)
	ReadInboundUsers(context.Context, string) (domain.InboundUsersObservation, error)
}

type UserObservationService struct {
	readers map[instances.InstanceID]UserObservationReader
}

func NewUserObservationService(readers map[instances.InstanceID]UserObservationReader) *UserObservationService {
	return &UserObservationService{readers: maps.Clone(readers)}
}

func (s *UserObservationService) reader(ctx context.Context, id instances.InstanceID) (UserObservationReader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reader, exists := s.readers[id]
	if !exists {
		return nil, instances.ErrNotFound
	}
	if reader == nil {
		return nil, domain.ErrAPIDisabled
	}
	return reader, nil
}

func (s *UserObservationService) GetInboundUser(ctx context.Context, id instances.InstanceID, selection domain.InboundUserSelection) (domain.InstanceInboundUserObservation, error) {
	reader, err := s.reader(ctx, id)
	if err != nil {
		return domain.InstanceInboundUserObservation{}, err
	}
	observation, err := reader.ReadInboundUser(ctx, selection)
	if cause := ctx.Err(); cause != nil {
		return domain.InstanceInboundUserObservation{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InstanceInboundUserObservation{}, err
	}
	return domain.InstanceInboundUserObservation{InstanceID: id, Selection: selection, Observation: observation}, nil
}

func (s *UserObservationService) ListInboundUsers(ctx context.Context, id instances.InstanceID, tag string) (domain.InstanceInboundUsersObservation, error) {
	reader, err := s.reader(ctx, id)
	if err != nil {
		return domain.InstanceInboundUsersObservation{}, err
	}
	observation, err := reader.ReadInboundUsers(ctx, tag)
	if cause := ctx.Err(); cause != nil {
		return domain.InstanceInboundUsersObservation{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InstanceInboundUsersObservation{}, err
	}
	// No credential comparison. Equal sort keys retain native order and duplicates;
	// anonymous native users remain members. These are independent live observations.
	slices.SortStableFunc(observation.Users, func(a, b domain.ObservedInboundUser) int {
		if order := cmp.Compare(a.Email, b.Email); order != 0 {
			return order
		}
		if order := cmp.Compare(a.Level, b.Level); order != 0 {
			return order
		}
		return cmp.Compare(a.Account.TypeName, b.Account.TypeName)
	})
	if err := ctx.Err(); err != nil {
		return domain.InstanceInboundUsersObservation{}, err
	}
	return domain.InstanceInboundUsersObservation{InstanceID: id, InboundTag: tag, Observation: observation}, nil
}
