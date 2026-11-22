// Package services coordinates exact traffic reads on a stable observed execution.
package services

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

// Reader receives a service-validated selection. Native response validation
// belongs to its implementation, independently of request validation.
type Reader interface {
	Read(context.Context, domain.Selection) (int64, error)
}

// Lister receives a service-validated scope and validates newly observed counters.
type Lister interface {
	List(context.Context, domain.Scope) ([]domain.Counter, error)
}
type StatusReader interface {
	Status(context.Context, string) (instances.Status, error)
}
type Binding struct {
	ID       string
	Reader   Reader
	Lister   Lister
	Resetter Resetter
}
type entry struct {
	reader   Reader
	lister   Lister
	resetter Resetter
	control  *control
	slots    chan struct{}
}
type Service struct {
	entries     map[string]entry
	status      StatusReader
	slots       chan struct{}
	snapshotsMu sync.Mutex
	snapshots   map[string]snapshot
	writes      chan struct{}
	epoch       string
}

// New admits 32 concurrent traffic reads globally and eight per instance,
// immediately rejecting overload. No slot or state lock queues native work.
func New(bindings []Binding, status StatusReader) (*Service, error) {
	if status == nil {
		return nil, domain.ErrInvalidArgument
	}
	s := &Service{entries: make(map[string]entry, len(bindings)), status: status, slots: make(chan struct{}, 32), snapshots: make(map[string]snapshot), writes: make(chan struct{}, 8), epoch: randomToken()}
	for _, b := range bindings {
		if instancevalidators.ID(b.ID) != nil {
			return nil, domain.ErrInvalidArgument
		}
		if _, exists := s.entries[b.ID]; exists {
			return nil, domain.ErrInvalidArgument
		}
		s.entries[b.ID] = entry{reader: b.Reader, lister: b.Lister, resetter: b.Resetter, control: &control{}, slots: make(chan struct{}, 8)}
	}
	return s, nil
}

func (s *Service) Read(ctx context.Context, id string, selection domain.Selection) (domain.Observation, error) {
	if err := ctx.Err(); err != nil {
		return domain.Observation{}, err
	}
	if instancevalidators.ID(id) != nil || validators.Selection(selection) != nil {
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
	value, readErr := selected.reader.Read(ctx, selection)
	after, err := s.status.Status(ctx, id)
	if err != nil {
		return domain.Observation{}, errors.Join(err, readErr)
	}
	if after.Execution.State != instances.Running || before.Execution.Generation != after.Execution.Generation {
		return domain.Observation{}, errors.Join(domain.ErrExecutionChanged, readErr)
	}
	if readErr != nil {
		return domain.Observation{}, readErr
	}
	if err := ctx.Err(); err != nil {
		return domain.Observation{}, err
	}
	return domain.Observation{InstanceID: id, Generation: after.Execution.Generation, Selection: selection, Bytes: value, Source: domain.Source, ObservedAt: time.Now().UTC(), Duration: time.Since(started)}, nil
}
