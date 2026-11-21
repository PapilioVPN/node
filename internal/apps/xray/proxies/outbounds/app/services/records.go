// Package services coordinates bounded native outbound observations.
package services

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type Client interface {
	Records(context.Context) ([]domain.Record, error)
}
type StatusReader interface {
	Status(context.Context, string) (instances.Status, error)
}
type Binding struct {
	ID     string
	Client Client
}
type entry struct {
	client Client
	reads  chan struct{}
}
type Service struct {
	entries map[string]entry
	status  StatusReader
	reads   chan struct{}
}

func New(bindings []Binding, status StatusReader) (*Service, error) {
	if status == nil {
		return nil, domain.ErrInvalidArgument
	}
	s := &Service{entries: make(map[string]entry, len(bindings)), status: status, reads: make(chan struct{}, 32)}
	for _, binding := range bindings {
		if instancevalidators.ID(binding.ID) != nil {
			return nil, domain.ErrInvalidArgument
		}
		if _, exists := s.entries[binding.ID]; exists {
			return nil, domain.ErrInvalidArgument
		}
		s.entries[binding.ID] = entry{binding.Client, make(chan struct{}, 8)}
	}
	return s, nil
}
func (s *Service) Records(ctx context.Context, id string) (domain.Records, error) {
	if err := ctx.Err(); err != nil {
		return domain.Records{}, err
	}
	if instancevalidators.ID(id) != nil {
		return domain.Records{}, domain.ErrInvalidArgument
	}
	selected, exists := s.entries[id]
	if !exists {
		return domain.Records{}, domain.ErrNotFound
	}
	if selected.client == nil {
		return domain.Records{}, domain.ErrUnsupported
	}
	select {
	case selected.reads <- struct{}{}:
	default:
		return domain.Records{}, domain.ErrCapacity
	}
	defer func() { <-selected.reads }()
	select {
	case s.reads <- struct{}{}:
	default:
		return domain.Records{}, domain.ErrCapacity
	}
	defer func() { <-s.reads }()
	before, err := s.status.Status(ctx, id)
	if err != nil {
		return domain.Records{}, err
	}
	if before.Execution.State != instances.Running {
		return domain.Records{}, domain.ErrNotRunning
	}
	started := time.Now()
	records, nativeErr := selected.client.Records(ctx)
	after, statusErr := s.status.Status(ctx, id)
	if statusErr != nil {
		return domain.Records{}, errors.Join(nativeErr, statusErr)
	}
	if after.Execution.State != instances.Running || after.Execution.Generation != before.Execution.Generation {
		return domain.Records{}, errors.Join(domain.ErrExecutionChanged, nativeErr)
	}
	if err := errors.Join(nativeErr, ctx.Err()); err != nil {
		return domain.Records{}, err
	}
	return domain.Records{InstanceID: id, Generation: before.Execution.Generation, Outbounds: records, ObservedAt: time.Now().UTC(), Duration: time.Since(started)}, nil
}
