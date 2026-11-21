// Package services coordinates selected-instance native runtime observations.
package services

import (
	"context"
	"errors"
	"time"

	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/domain"
)

type Reader interface {
	Read(context.Context) (domain.Statistics, error)
}
type StatusReader interface {
	Status(context.Context, string) (instances.Status, error)
}
type Binding struct {
	ID     string
	Reader Reader
}
type entry struct {
	reader Reader
	slots  chan struct{}
}
type Service struct {
	entries map[string]entry
	status  StatusReader
	slots   chan struct{}
}

// New bounds whole reads to 32 globally and eight per endpoint. Overload rejects
// immediately; it does not queue repeated native ReadMemStats work in Node.
func New(bindings []Binding, status StatusReader) (*Service, error) {
	if status == nil {
		return nil, domain.ErrInvalidArgument
	}
	service := &Service{entries: make(map[string]entry, len(bindings)), status: status, slots: make(chan struct{}, 32)}
	for _, binding := range bindings {
		if instancevalidators.ID(binding.ID) != nil {
			return nil, domain.ErrInvalidArgument
		}
		if _, exists := service.entries[binding.ID]; exists {
			return nil, domain.ErrInvalidArgument
		}
		service.entries[binding.ID] = entry{reader: binding.Reader, slots: make(chan struct{}, 8)}
	}
	return service, nil
}
func (s *Service) Read(ctx context.Context, id string) (domain.Observation, error) {
	if err := ctx.Err(); err != nil {
		return domain.Observation{}, err
	}
	if instancevalidators.ID(id) != nil {
		return domain.Observation{}, domain.ErrInvalidArgument
	}
	selected, exists := s.entries[id]
	if !exists {
		return domain.Observation{}, domain.ErrNotFound
	}
	if selected.reader == nil {
		return domain.Observation{}, domain.ErrUnsupported
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return domain.Observation{}, domain.ErrCapacity
	}
	defer func() { <-s.slots }()
	select {
	case selected.slots <- struct{}{}:
	default:
		return domain.Observation{}, domain.ErrCapacity
	}
	defer func() { <-selected.slots }()
	started := time.Now()
	before, err := s.status.Status(ctx, id)
	if err != nil {
		return domain.Observation{}, err
	}
	if before.Execution.State != instances.Running {
		return domain.Observation{}, domain.ErrNotRunning
	}
	row, readErr := selected.reader.Read(ctx)
	after, err := s.status.Status(ctx, id)
	if err != nil {
		return domain.Observation{}, errors.Join(err, readErr)
	}
	if after.Execution.Generation != before.Execution.Generation || after.Execution.State != instances.Running {
		return domain.Observation{}, errors.Join(domain.ErrExecutionChanged, readErr)
	}
	if readErr != nil {
		return domain.Observation{}, readErr
	}
	if err := ctx.Err(); err != nil {
		return domain.Observation{}, err
	}
	return domain.Observation{InstanceID: id, Generation: after.Execution.Generation, Statistics: row, ObservedAt: time.Now().UTC(), Duration: time.Since(started)}, nil
}
