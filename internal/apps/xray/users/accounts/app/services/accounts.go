// Package services coordinates live account observations and control with bounded native work.
package services

import (
	"context"
	"errors"
	"sync"
	"time"

	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

// Client uses service-validated selectors. Get receives a nonempty exact email,
// since an empty native selector would become a collection request.
type Client interface {
	Count(context.Context, string) (int64, error)
	Get(context.Context, string, string) (*domain.Record, error)
	Add(context.Context, string, []byte) (domain.NativeResult, error)
	Remove(context.Context, string, string) (domain.NativeResult, error)
}

// CollectionReader validates fresh native responses. Records transfers an owned
// collection within the native read bounds. GetMetadata receives the same
// service-validated nonempty exact selector as Client.Get.
type CollectionReader interface {
	Records(context.Context, string) (domain.NativeRecords, error)
	Metadata(context.Context, string) (domain.NativeMetadata, error)
	GetMetadata(context.Context, string, string) (*domain.Metadata, error)
}
type StatusReader interface {
	Status(context.Context, string) (instances.Status, error)
}
type Binding struct {
	Reader CollectionReader
	ID     string
	Client Client
}
type entry struct {
	reader              CollectionReader
	client              Client
	mu                  sync.Mutex
	readers             int
	mutating            bool
	uncertainGeneration uint64
	last                *domain.Operation
}
type Service struct {
	inbounds         InboundReader
	inspector        AccountInspector
	pages            observationStore
	entries          map[string]*entry
	status           StatusReader
	reads, mutations chan struct{}
}

// New admits up to 32 reads globally and eight per instance without queueing.
func New(bindings []Binding, status StatusReader, inbounds InboundReader, inspector AccountInspector) (*Service, error) {
	if status == nil || inbounds == nil || inspector == nil {
		return nil, domain.ErrInvalidArgument
	}
	s := &Service{inbounds: inbounds, inspector: inspector, entries: make(map[string]*entry, len(bindings)), status: status, reads: make(chan struct{}, 32), mutations: make(chan struct{}, 8)}
	for _, b := range bindings {
		if instancevalidators.ID(b.ID) != nil {
			return nil, domain.ErrInvalidArgument
		}
		if _, ok := s.entries[b.ID]; ok {
			return nil, domain.ErrInvalidArgument
		}
		s.entries[b.ID] = &entry{client: b.Client, reader: b.Reader}
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
	case s.reads <- struct{}{}:
	default:
		return nil, observation, nil, domain.ErrCapacity
	}
	releaseGlobal := func() { <-s.reads }
	state, err := s.status.Status(ctx, id)
	if err != nil {
		releaseGlobal()
		return nil, observation, nil, err
	}
	if state.Execution.State != instances.Running {
		releaseGlobal()
		return nil, observation, nil, domain.ErrNotRunning
	}
	selected.mu.Lock()
	if selected.uncertainGeneration != 0 && selected.uncertainGeneration == state.Execution.Generation {
		selected.mu.Unlock()
		releaseGlobal()
		return nil, observation, nil, domain.ErrUncertain
	}
	if selected.mutating || selected.readers >= 8 {
		selected.mu.Unlock()
		releaseGlobal()
		return nil, observation, nil, domain.ErrCapacity
	}
	selected.readers++
	selected.mu.Unlock()
	release := func() { selected.mu.Lock(); selected.readers--; selected.mu.Unlock(); releaseGlobal() }
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

// ReadMembership returns one native collection for an in-process consumer.
func (s *Service) ReadMembership(ctx context.Context, id, tag string) (domain.Membership, error) {
	_, observation, release, err := s.begin(ctx, id, tag)
	if err != nil {
		return domain.Membership{}, err
	}
	defer release()
	reader := s.entries[id].reader
	if reader == nil {
		return domain.Membership{}, domain.ErrUnsupported
	}
	started := time.Now()
	records, err := reader.Records(ctx, tag)
	if err = s.finish(ctx, &observation, started, err); err != nil {
		return domain.Membership{}, err
	}
	return domain.Membership{Observation: observation, Users: records.Users}, nil
}
