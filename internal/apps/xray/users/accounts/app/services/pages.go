package services

import (
	"context"
	"crypto/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

const observationLifetime = 30 * time.Second

type retainedObservation struct {
	observation domain.Observation
	epoch       uint64
	export      bool
	size        int
	records     []domain.Record
	metadata    []domain.Metadata
	cost        domain.ReadCost
	expires     time.Time
	bytes       int
	token       string
}
type observationStore struct {
	mu             sync.Mutex
	snapshots      map[string]*retainedObservation
	pending, bytes int
}

func (p *retainedObservation) count() int {
	if p.export {
		return len(p.records)
	}
	return len(p.metadata)
}
func pageSize(r domain.PageRequest) (int, error) {
	if r.Size > domain.MaxPageSize || len(r.Cursor) > 96 {
		return 0, domain.ErrInvalidArgument
	}
	if r.Size == 0 {
		return domain.MaxPageSize, nil
	}
	return int(r.Size), nil
}

// capture reserves capacity before the complete native read. Credential exports
// and metadata observations share eight pending/retained slots and 32 MiB of
// logical storage. Protobuf/native heap and concurrent response copies are extra.
func (s *Service) capture(ctx context.Context, id, tag string, r domain.PageRequest, export bool, load func(CollectionReader, *retainedObservation) error) (*retainedObservation, int, error) {
	size, err := pageSize(r)
	if err != nil {
		return nil, 0, err
	}
	_, obs, release, err := s.begin(ctx, id, tag)
	if err != nil {
		return nil, 0, err
	}
	defer release()
	entry := s.entries[id]
	entry.mu.Lock()
	epoch := entry.epoch
	entry.mu.Unlock()
	if r.Cursor != "" {
		token, index, ok := strings.Cut(r.Cursor, ":")
		if !ok {
			return nil, 0, domain.ErrInvalidArgument
		}
		offset, err := strconv.Atoi(index)
		if err != nil || offset <= 0 {
			return nil, 0, domain.ErrInvalidArgument
		}
		s.pages.mu.Lock()
		snapshot := s.pages.snapshots[token]
		s.pages.mu.Unlock()
		if snapshot == nil || !time.Now().Before(snapshot.expires) || snapshot.observation.InstanceID != id || snapshot.observation.InboundTag != tag || snapshot.export != export || snapshot.size != size || snapshot.observation.Generation != obs.Generation || snapshot.epoch != epoch || offset >= snapshot.count() {
			return nil, 0, domain.ErrStale
		}
		if err = s.finish(ctx, &obs, time.Now(), nil); err != nil {
			return nil, 0, err
		}
		return snapshot, offset, nil
	}
	if entry.reader == nil {
		return nil, 0, domain.ErrUnsupported
	}
	s.pages.mu.Lock()
	if s.pages.snapshots == nil {
		s.pages.snapshots = make(map[string]*retainedObservation)
	}
	for token, p := range s.pages.snapshots {
		if !time.Now().Before(p.expires) {
			delete(s.pages.snapshots, token)
			s.pages.bytes -= p.bytes
		}
	}
	if len(s.pages.snapshots)+s.pages.pending >= domain.MaxObservations {
		s.pages.mu.Unlock()
		return nil, 0, domain.ErrCapacity
	}
	s.pages.pending++
	s.pages.mu.Unlock()
	defer func() { s.pages.mu.Lock(); s.pages.pending--; s.pages.mu.Unlock() }()
	snapshot := &retainedObservation{observation: obs, epoch: epoch, export: export, size: size}
	started := time.Now()
	if err := load(entry.reader, snapshot); err != nil {
		return nil, 0, err
	}
	if err = s.finish(ctx, &snapshot.observation, started, nil); err != nil {
		return nil, 0, err
	}
	snapshot.expires = time.Now().UTC().Add(observationLifetime)
	if snapshot.count() > domain.MaxCollectionRecords || snapshot.bytes > domain.MaxRetainedBytes {
		return nil, 0, domain.ErrCapacity
	}
	if snapshot.count() > size {
		snapshot.token = rand.Text()
		snapshot.observation.InstanceID = strings.Clone(id)
		snapshot.observation.InboundTag = strings.Clone(tag)
		s.pages.mu.Lock()
		defer s.pages.mu.Unlock()
		if s.pages.bytes+snapshot.bytes > domain.MaxRetainedBytes {
			return nil, 0, domain.ErrCapacity
		}
		s.pages.snapshots[snapshot.token] = snapshot
		s.pages.bytes += snapshot.bytes
	}
	return snapshot, 0, nil
}
func evidence(p *retainedObservation, end int) domain.PageEvidence {
	result := domain.PageEvidence{ExpiresAt: p.expires, ReadCost: p.cost}
	if end < p.count() {
		result.NextCursor = p.token + ":" + strconv.Itoa(end)
	}
	return result
}
func (s *Service) ListMetadata(ctx context.Context, id, tag string, r domain.PageRequest) (domain.MetadataPage, error) {
	p, offset, err := s.capture(ctx, id, tag, r, false, func(reader CollectionReader, p *retainedObservation) error {
		result, err := reader.Metadata(ctx, tag)
		if err != nil {
			return err
		}
		p.metadata = result.Users
		p.cost = result.ReadCost
		for _, u := range p.metadata {
			p.bytes += len(u.Email) + len(u.AccountType) + 32
		}
		return nil
	})
	if err != nil {
		return domain.MetadataPage{}, err
	}
	end := min(offset+p.size, len(p.metadata))
	users := append([]domain.Metadata{}, p.metadata[offset:end]...)
	return domain.MetadataPage{Observation: p.observation, Users: users, PageEvidence: evidence(p, end)}, nil
}
func (s *Service) Export(ctx context.Context, id, tag string, r domain.PageRequest) (domain.AccountPage, error) {
	p, offset, err := s.capture(ctx, id, tag, r, true, func(reader CollectionReader, p *retainedObservation) error {
		result, err := reader.Records(ctx, tag)
		if err != nil {
			return err
		}
		p.records = result.Users
		p.cost = result.ReadCost
		for _, u := range p.records {
			p.bytes += len(u.Email) + len(u.Configuration) + 32
		}
		return nil
	})
	if err != nil {
		return domain.AccountPage{}, err
	}
	end := min(offset+p.size, len(p.records))
	users := make([]domain.Record, end-offset)
	for i, u := range p.records[offset:end] {
		users[i] = u
		users[i].Configuration = append([]byte(nil), u.Configuration...)
	}
	return domain.AccountPage{Observation: p.observation, Users: users, PageEvidence: evidence(p, end)}, nil
}
func (s *Service) GetMetadata(ctx context.Context, id, tag, email string) (domain.MetadataAccount, error) {
	if err := validators.Selector(email); err != nil {
		return domain.MetadataAccount{}, err
	}
	_, obs, release, err := s.begin(ctx, id, tag)
	if err != nil {
		return domain.MetadataAccount{}, err
	}
	defer release()
	if s.entries[id].reader == nil {
		return domain.MetadataAccount{}, domain.ErrUnsupported
	}
	started := time.Now()
	record, err := s.entries[id].reader.GetMetadata(ctx, tag, email)
	if err = s.finish(ctx, &obs, started, err); err != nil {
		return domain.MetadataAccount{}, err
	}
	return domain.MetadataAccount{Observation: obs, RequestedEmail: email, User: record}, nil
}
