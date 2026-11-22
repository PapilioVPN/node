// Package services coordinates focused account reads with bounded native work.
package services

import (
	"context"
	"errors"
	"time"

	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

// Client uses native count and exact lookup facilities, never a collection scan.
type Client interface {
	Count(context.Context, string) (int64, error)
	Get(context.Context, string, string) (*domain.Record, error)
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

// New admits up to 32 reads globally and eight per instance without queueing.
func New(bindings []Binding, status StatusReader) (*Service, error) {
	if status == nil {
		return nil, domain.ErrInvalidArgument
	}
	s := &Service{entries: make(map[string]entry, len(bindings)), status: status, reads: make(chan struct{}, 32)}
	for _, b := range bindings {
		if instancevalidators.ID(b.ID) != nil {
			return nil, domain.ErrInvalidArgument
		}
		if _, ok := s.entries[b.ID]; ok {
			return nil, domain.ErrInvalidArgument
		}
		s.entries[b.ID] = entry{b.Client, make(chan struct{}, 8)}
	}
	return s, nil
}
func (s *Service) begin(ctx context.Context, id, tag string) (Client, domain.Observation, func(), error) {
	var observation domain.Observation
	if err := ctx.Err(); err != nil {
		return nil, observation, nil, err
	}
	if instancevalidators.ID(id) != nil || validators.Selector(tag) != nil {
		return nil, observation, nil, domain.ErrInvalidArgument
	}
	selected, ok := s.entries[id]
	if !ok {
		return nil, observation, nil, domain.ErrNotFound
	}
	if selected.client == nil {
		return nil, observation, nil, domain.ErrUnsupported
	}
	select {
	case selected.reads <- struct{}{}:
	default:
		return nil, observation, nil, domain.ErrCapacity
	}
	select {
	case s.reads <- struct{}{}:
	default:
		<-selected.reads
		return nil, observation, nil, domain.ErrCapacity
	}
	release := func() { <-selected.reads; <-s.reads }
	state, err := s.status.Status(ctx, id)
	if err != nil {
		release()
		return nil, observation, nil, err
	}
	if state.Execution.State != instances.Running {
		release()
		return nil, observation, nil, domain.ErrNotRunning
	}
	observation = domain.Observation{InstanceID: id, InboundTag: tag, Generation: state.Execution.Generation}
	return selected.client, observation, release, nil
}
func (s *Service) finish(ctx context.Context, observation *domain.Observation, started time.Time, cause error) error {
	after, err := s.status.Status(ctx, observation.InstanceID)
	if err != nil {
		return errors.Join(cause, err)
	}
	if after.Execution.State != instances.Running || after.Execution.Generation != observation.Generation {
		return errors.Join(domain.ErrExecutionChanged, cause)
	}
	observation.ObservedAt = time.Now().UTC()
	observation.Duration = time.Since(started)
	return errors.Join(cause, ctx.Err())
}
func (s *Service) Count(ctx context.Context, id, tag string) (domain.Count, error) {
	client, observation, release, err := s.begin(ctx, id, tag)
	if err != nil {
		return domain.Count{}, err
	}
	defer release()
	started := time.Now()
	count, err := client.Count(ctx, tag)
	if err = s.finish(ctx, &observation, started, err); err != nil {
		return domain.Count{}, err
	}
	return domain.Count{Observation: observation, Count: count}, nil
}
func (s *Service) Get(ctx context.Context, id, tag, email string) (domain.Account, error) {
	if err := validators.Selector(email); err != nil {
		return domain.Account{}, err
	}
	client, observation, release, err := s.begin(ctx, id, tag)
	if err != nil {
		return domain.Account{}, err
	}
	defer release()
	started := time.Now()
	record, err := client.Get(ctx, tag, email)
	if err = s.finish(ctx, &observation, started, err); err != nil {
		return domain.Account{}, err
	}
	return domain.Account{Observation: observation, RequestedEmail: email, User: record}, nil
}
