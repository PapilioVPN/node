package services

import (
	"context"
	"errors"
	"strings"
	"time"

	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

// mutation reserves one instance and actual native-call capacity. No state lock
// spans native work; finish joins the reservation with its retained evidence.
type mutation struct {
	service         *Service
	selected        *entry
	generation      uint64
	permits         int
	started         time.Time
	requiresRestart bool
}

func (s *Service) reserveMutation(ctx context.Context, id string, permits int) (m mutation, err error) {
	m.service, m.permits = s, permits
	if err := ctx.Err(); err != nil {
		return m, err
	}
	if instancevalidators.ID(id) != nil || permits < 1 || permits > cap(s.mutations) {
		return m, domain.ErrInvalidArgument
	}
	m.selected = s.entries[id]
	if m.selected == nil {
		return m, domain.ErrNotFound
	}
	if m.selected.client == nil {
		return m, domain.ErrUnsupported
	}
	acquired := 0
	defer func() {
		if err != nil {
			for range acquired {
				<-s.mutations
			}
		}
	}()
	for range permits {
		select {
		case s.mutations <- struct{}{}:
			acquired++
		default:
			return m, domain.ErrCapacity
		}
	}
	before, err := s.status.Status(ctx, id)
	if err != nil {
		return m, err
	}
	m.generation = before.Execution.Generation
	if before.Execution.State != instances.Running {
		return m, domain.ErrNotRunning
	}
	m.selected.mu.Lock()
	defer m.selected.mu.Unlock()
	if m.selected.uncertainGeneration != 0 && m.selected.uncertainGeneration == m.generation {
		m.requiresRestart = true
		return m, domain.ErrUncertain
	}
	if m.selected.mutating || m.selected.readers != 0 {
		return m, domain.ErrCapacity
	}
	m.selected.mutating = true
	m.selected.epoch++
	m.started = time.Now()
	return m, nil
}

func (m mutation) finish(operation *domain.Operation) {
	operation.ObservedAt = time.Now().UTC()
	operation.Duration = time.Since(m.started)
	m.selected.mu.Lock()
	m.selected.mutating = false
	if operation.RequiresInstanceRestart {
		m.selected.uncertainGeneration = operation.Generation
	}
	copy := *operation
	copy.InstanceID = strings.Clone(operation.InstanceID)
	copy.InboundTag = strings.Clone(operation.InboundTag)
	copy.Email = strings.Clone(operation.Email)
	m.selected.last = &copy
	m.selected.mu.Unlock()
	for range m.permits {
		<-m.service.mutations
	}
}

func (s *Service) observeMutation(ctx context.Context, operation *domain.Operation, nativeErr error) error {
	after, statusErr := s.status.Status(ctx, operation.InstanceID)
	if statusErr != nil {
		if operation.Acknowledged {
			operation.Effect = domain.Unknown
		}
		return errors.Join(nativeErr, statusErr)
	}
	if after.Execution.State != instances.Running || after.Execution.Generation != operation.Generation {
		if operation.Effect != domain.NotApplied {
			operation.Effect = domain.Unknown
		}
		return errors.Join(domain.ErrExecutionChanged, nativeErr)
	}
	return nativeErr
}
