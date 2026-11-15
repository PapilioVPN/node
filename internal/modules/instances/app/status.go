package app

import (
	"context"
	"maps"

	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

// StatusService borrows the existing lifecycles through an immutable identity index.
// Construction copies bindings once; each read selects one runtime without OS I/O.
type StatusService struct {
	instances map[domain.InstanceID]*Lifecycle
}

func NewStatusService(instances map[domain.InstanceID]*Lifecycle) *StatusService {
	return &StatusService{instances: maps.Clone(instances)}
}

func (s *StatusService) GetInstanceStatus(ctx context.Context, id domain.InstanceID) (domain.Status, error) {
	if err := ctx.Err(); err != nil {
		return domain.Status{}, err
	}
	instance, exists := s.instances[id]
	if !exists {
		return domain.Status{}, domain.ErrNotFound
	}
	return instance.Status(ctx)
}
