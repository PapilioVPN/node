package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
)

const pageLifetime = 30 * time.Second

// Each projection retains at most eight snapshots and 32 MiB of record content,
// across all instances. Metadata/allocation overhead is additional. No workers,
// cache refresh or full native read per page. A fresh first page for the same
// instance/projection can supersede an earlier continuation.
type pageStore[T any] struct {
	mu        sync.Mutex
	snapshots map[string]snapshot[T]
}
type snapshot[T any] struct {
	token             string
	generation, epoch uint64
	items             []T
	bytes             int
	observed          time.Time
	duration          time.Duration
	expires           time.Time
}

func pageSize(input domain.PageRequest) (int, error) {
	if input.Size > 50 || len(input.Cursor) > 96 {
		return 0, domain.ErrInvalidArgument
	}
	if input.Size == 0 {
		return 50, nil
	}
	return int(input.Size), nil
}
func (s *pageStore[T]) page(id string, input domain.PageRequest, generation, epoch uint64, load func() ([]T, int, error), clone func(T) T) (items []T, evidence domain.PageEvidence, at time.Time, duration time.Duration, err error) {
	size, err := pageSize(input)
	if err != nil {
		return nil, evidence, at, 0, err
	}
	var snap snapshot[T]
	offset := 0
	if input.Cursor != "" {
		token, index, ok := strings.Cut(input.Cursor, ":")
		if !ok {
			return nil, evidence, at, 0, domain.ErrInvalidArgument
		}
		offset, err = strconv.Atoi(index)
		if err != nil || offset <= 0 {
			return nil, evidence, at, 0, domain.ErrInvalidArgument
		}
		s.mu.Lock()
		snap = s.snapshots[id]
		s.mu.Unlock()
		if snap.token != token || token == "" || !time.Now().Before(snap.expires) || snap.generation != generation || snap.epoch != epoch || offset >= len(snap.items) {
			return nil, evidence, at, 0, domain.ErrStale
		}
	} else {
		started := time.Now()
		all, bytes, err := load()
		if err != nil {
			return nil, evidence, at, 0, err
		}
		if len(all) > domain.MaxRecords || bytes > domain.MaxResponseBytes {
			return nil, evidence, at, 0, domain.ErrCapacity
		}
		snap = snapshot[T]{generation: generation, epoch: epoch, items: all, bytes: bytes, observed: time.Now().UTC(), duration: time.Since(started), expires: time.Now().UTC().Add(pageLifetime)}
		if len(all) > size {
			key := make([]byte, 16)
			if _, err := rand.Read(key); err != nil {
				return nil, evidence, at, 0, err
			}
			snap.token = hex.EncodeToString(key)
			s.mu.Lock()
			if s.snapshots == nil {
				s.snapshots = make(map[string]snapshot[T])
			}
			total := bytes
			for key, old := range s.snapshots {
				if !time.Now().Before(old.expires) {
					delete(s.snapshots, key)
				} else if key != id {
					total += old.bytes
				}
			}
			_, exists := s.snapshots[id]
			if (!exists && len(s.snapshots) >= 8) || total > domain.MaxResponseBytes {
				s.mu.Unlock()
				return nil, evidence, at, 0, domain.ErrCapacity
			}
			s.snapshots[strings.Clone(id)] = snap
			s.mu.Unlock()
		}
	}
	end := min(offset+size, len(snap.items))
	items = make([]T, end-offset)
	for i := range items {
		items[i] = clone(snap.items[offset+i])
	}
	if end < len(snap.items) {
		evidence.NextCursor = snap.token + ":" + strconv.Itoa(end)
	}
	evidence.ExpiresAt = snap.expires
	return items, evidence, snap.observed, snap.duration, nil
}
func (s *Service) RecordPage(ctx context.Context, id string, input domain.PageRequest) (domain.Records, error) {
	selected, generation, release, err := s.beginRead(ctx, id)
	if err != nil {
		return domain.Records{}, err
	}
	defer release()
	selected.mu.Lock()
	epoch := selected.epoch
	selected.mu.Unlock()
	items, page, at, duration, err := s.records.page(id, input, generation, epoch, func() ([]domain.Record, int, error) {
		records, err := selected.client.Records(ctx)
		bytes := 0
		for _, r := range records {
			bytes += len(r.Tag) + len(r.Configuration)
		}
		return records, bytes, err
	}, func(r domain.Record) domain.Record {
		return domain.Record{Tag: r.Tag, Configuration: append([]byte(nil), r.Configuration...)}
	})
	if err = s.finishRead(ctx, id, generation, err); err != nil {
		return domain.Records{}, err
	}
	return domain.Records{InstanceID: id, Generation: generation, Inbounds: items, ObservedAt: at, Duration: duration, PageEvidence: page}, nil
}
func (s *Service) TagPage(ctx context.Context, id string, input domain.PageRequest) (domain.Tags, error) {
	selected, generation, release, err := s.beginRead(ctx, id)
	if err != nil {
		return domain.Tags{}, err
	}
	defer release()
	selected.mu.Lock()
	epoch := selected.epoch
	selected.mu.Unlock()
	items, page, at, duration, err := s.tags.page(id, input, generation, epoch, func() ([]string, int, error) {
		tags, err := selected.client.Tags(ctx)
		bytes := 0
		for _, tag := range tags {
			bytes += len(tag)
		}
		return tags, bytes, err
	}, func(s string) string { return s })
	if err = s.finishRead(ctx, id, generation, err); err != nil {
		return domain.Tags{}, err
	}
	return domain.Tags{InstanceID: id, Generation: generation, Tags: items, ObservedAt: at, Duration: duration, PageEvidence: page}, nil
}

// Inspector belongs to the consuming capability; native protobuf interpretation
// stays in its concrete adapter, rather than application or Accounts code.
type Inspector interface {
	Candidate([]byte) (domain.Summary, string, error)
	Inspect([]byte) (domain.Summary, string, error)
	Fingerprint([]byte) (string, error)
}
type Catalog struct {
	plansMu   sync.Mutex
	plans     map[string]replacementCandidate
	service   *Service
	inspector Inspector
	pages     pageStore[domain.Summary]
}

func NewCatalog(service *Service, inspector Inspector) *Catalog {
	return &Catalog{service: service, inspector: inspector}
}
func (c *Catalog) List(ctx context.Context, id string, input domain.PageRequest) (domain.Summaries, error) {
	selected, generation, release, err := c.service.beginRead(ctx, id)
	if err != nil {
		return domain.Summaries{}, err
	}
	defer release()
	selected.mu.Lock()
	epoch := selected.epoch
	selected.mu.Unlock()
	items, page, at, duration, err := c.pages.page(id, input, generation, epoch, func() ([]domain.Summary, int, error) {
		if selected.projection == nil {
			return nil, 0, domain.ErrUnsupported
		}
		summaries, err := selected.projection.Summaries(ctx)
		if err != nil {
			return nil, 0, err
		}
		if len(summaries) > domain.MaxRecords {
			return nil, 0, domain.ErrCapacity
		}
		bytes := 0
		for _, item := range summaries {
			bytes += len(item.Tag) + len(item.NativeType) + len(item.AccountType) + 256
		}
		return summaries, bytes, nil
	}, func(s domain.Summary) domain.Summary { return s })
	if err = c.service.finishRead(ctx, id, generation, err); err != nil {
		return domain.Summaries{}, err
	}
	return domain.Summaries{InstanceID: id, Generation: generation, Inbounds: items, PageEvidence: page, ObservedAt: at, Duration: duration}, nil
}
