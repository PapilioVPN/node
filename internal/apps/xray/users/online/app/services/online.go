// Package services coordinates fresh, instance-scoped native online observations.
package services

import (
	"context"
	"errors"
	"time"

	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
)

type Reader interface {
	Count(context.Context, string) (domain.Summary, error)
	IPs(context.Context, string) (domain.Visitors, error)
}
type StatusReader interface {
	Status(context.Context, string) (instances.Status, error)
}
type Binding struct {
	ID         string
	Reader     Reader
	Collection CollectionReader
}
type entry struct {
	reader     Reader
	collection CollectionReader
	slots      chan struct{}
}
type Service struct {
	entries map[string]entry
	status  StatusReader
	slots   chan struct{}
	pages   pageStore
}

// New admits 32 reads globally and eight per instance across both operations,
// rejecting overload immediately. It owns bounded short-lived page observations,
// with no native channel, background refresh or durable history.
func New(bindings []Binding, status StatusReader) (*Service, error) {
	if status == nil {
		return nil, domain.ErrInvalidArgument
	}
	s := &Service{entries: make(map[string]entry, len(bindings)), status: status, slots: make(chan struct{}, 32), pages: pageStore{observations: make(map[string]*retained)}}
	for _, b := range bindings {
		if instancevalidators.ID(b.ID) != nil {
			return nil, domain.ErrInvalidArgument
		}
		if _, ok := s.entries[b.ID]; ok {
			return nil, domain.ErrInvalidArgument
		}
		s.entries[b.ID] = entry{reader: b.Reader, collection: b.Collection, slots: make(chan struct{}, 8)}
	}
	return s, nil
}

func (s *Service) Count(ctx context.Context, selection domain.Selection) (domain.Status, error) {
	var summary domain.Summary
	observation, err := s.observe(ctx, selection, func(r Reader) error {
		var err error
		summary, err = r.Count(ctx, selection.Email)
		return err
	})
	if err != nil {
		return domain.Status{}, err
	}
	return domain.Status{Observation: observation, Summary: summary}, nil
}
func (s *Service) IPs(ctx context.Context, selection domain.Selection) (domain.IPList, error) {
	var visitors domain.Visitors
	observation, err := s.observe(ctx, selection, func(r Reader) error {
		var err error
		visitors, err = r.IPs(ctx, selection.Email)
		return err
	})
	if err != nil {
		return domain.IPList{}, err
	}
	if len(visitors.IPs) > domain.MaxIPs {
		return domain.IPList{}, domain.ErrCapacity
	}
	return domain.IPList{Observation: observation, Visitors: visitors}, nil
}

func (s *Service) observe(ctx context.Context, selection domain.Selection, read func(Reader) error) (domain.Observation, error) {
	if err := ctx.Err(); err != nil {
		return domain.Observation{}, err
	}
	if err := validateSelection(selection); err != nil {
		return domain.Observation{}, err
	}
	return s.observeRead(ctx, selection, read)
}

func (s *Service) observeRead(ctx context.Context, selection domain.Selection, read func(Reader) error) (domain.Observation, error) {
	selected, exists := s.entries[selection.InstanceID]
	if !exists {
		return domain.Observation{}, domain.ErrNotFound
	}
	if selected.reader == nil {
		return domain.Observation{}, domain.ErrNotConfigured
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
	before, err := s.status.Status(ctx, selection.InstanceID)
	if err != nil {
		return domain.Observation{}, err
	}
	if before.Execution.State != instances.Running {
		return domain.Observation{}, domain.ErrNotRunning
	}
	readErr := read(selected.reader)
	after, err := s.status.Status(ctx, selection.InstanceID)
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
	return domain.Observation{Selection: selection, Generation: after.Execution.Generation, ObservedAt: time.Now().UTC(), Duration: time.Since(started)}, nil
}
