package app

import (
	"context"
	"maps"

	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

// LifecycleService borrows the existing lifecycles through an immutable identity index.
// Construction copies bindings once; operations select only the requested runtime.
type LifecycleService struct {
	instances map[domain.InstanceID]*Lifecycle
}

func NewLifecycleService(instances map[domain.InstanceID]*Lifecycle) *LifecycleService {
	return &LifecycleService{instances: maps.Clone(instances)}
}

func (s *LifecycleService) GetInstanceStatus(ctx context.Context, id domain.InstanceID) (domain.Status, error) {
	instance, err := s.lookup(ctx, id)
	if err != nil {
		return domain.Status{}, err
	}
	return instance.Status(ctx)
}

// StartInstance admits the selected runtime without changing its ownership.
// Successful spawning is not a readiness claim; execution outlives this request.
func (s *LifecycleService) StartInstance(ctx context.Context, id domain.InstanceID) (domain.Status, error) {
	instance, err := s.lookup(ctx, id)
	if err != nil {
		return domain.Status{}, err
	}
	return instance.Start(ctx)
}

// StopInstance preserves the observed partial outcome and the native stop cause.
// The resource owner retains final cleanup responsibility after cancellation.
func (s *LifecycleService) StopInstance(ctx context.Context, id domain.InstanceID) (domain.Status, error) {
	instance, err := s.lookup(ctx, id)
	if err != nil {
		return domain.Status{}, err
	}
	return instance.Stop(ctx)
}

func (s *LifecycleService) lookup(ctx context.Context, id domain.InstanceID) (*Lifecycle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	instance, exists := s.instances[id]
	if !exists {
		return nil, domain.ErrNotFound
	}
	return instance, nil
}
