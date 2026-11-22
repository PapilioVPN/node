package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type snapshot struct {
	page    domain.Page
	expires time.Time
	bytes   int
}

func randomToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (s *Service) generation(ctx context.Context, id string) (uint64, error) {
	status, err := s.status.Status(ctx, id)
	if err != nil {
		return 0, err
	}
	if status.Execution.State != instances.Running {
		return 0, domain.ErrNotRunning
	}
	return status.Execution.Generation, nil
}
func pageSize(size uint32) (int, error) {
	if size > domain.MaxPageSize {
		return 0, domain.ErrInvalidArgument
	}
	if size == 0 {
		size = domain.MaxPageSize
	}
	return int(size), nil
}
func (s *Service) List(ctx context.Context, id string, request domain.PageRequest) (domain.Page, error) {
	size, err := pageSize(request.PageSize)
	if err != nil || instancevalidators.ID(id) != nil || validators.Selection(domain.Selection{Scope: request.Scope, Name: "scope", Direction: domain.Uplink}) != nil || len(request.PageToken) > 128 {
		return domain.Page{}, domain.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	selected, ok := s.entries[id]
	if !ok {
		return domain.Page{}, domain.ErrNotFound
	}
	if request.PageToken != "" {
		return s.continuePage(ctx, id, request, size)
	}
	if selected.lister == nil {
		return domain.Page{}, domain.ErrUnsupported
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return domain.Page{}, domain.ErrCapacity
	}
	defer func() { <-s.slots }()
	select {
	case selected.slots <- struct{}{}:
	default:
		return domain.Page{}, domain.ErrCapacity
	}
	defer func() { <-selected.slots }()
	started := time.Now()
	generation, err := s.generation(ctx, id)
	if err != nil {
		return domain.Page{}, err
	}
	records, err := selected.lister.List(ctx, request.Scope)
	if err != nil {
		return domain.Page{}, err
	}
	after, err := s.generation(ctx, id)
	if err != nil {
		return domain.Page{}, err
	}
	if generation != after {
		return domain.Page{}, domain.ErrExecutionChanged
	}
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	if len(records) > domain.MaxCounters {
		return domain.Page{}, domain.ErrCapacity
	}
	retainedBytes := 0
	for i := range records {
		retainedBytes += len(records[i].Selection.Name) + 64
	}
	if retainedBytes > domain.MaxCollectionBytes {
		return domain.Page{}, domain.ErrCapacity
	}
	result := domain.Page{InstanceID: id, Generation: generation, Scope: request.Scope, Counters: records, Source: domain.CollectionSource, ObservedAt: time.Now().UTC(), Duration: time.Since(started)}
	if len(records) <= size {
		s.snapshotsMu.Lock()
		for key, old := range s.snapshots {
			if old.page.InstanceID == id {
				delete(s.snapshots, key)
			}
		}
		s.snapshotsMu.Unlock()
		result.Counters = append([]domain.Counter{}, records...)
		return result, nil
	}
	s.snapshotsMu.Lock()
	defer s.snapshotsMu.Unlock()
	used := 0
	for key, old := range s.snapshots {
		if time.Now().After(old.expires) || old.page.InstanceID == id {
			delete(s.snapshots, key)
		} else {
			used += old.bytes
		}
	}
	if len(s.snapshots) >= 32 || used+retainedBytes > domain.MaxCollectionBytes {
		return domain.Page{}, domain.ErrCapacity
	}
	token := randomToken()
	s.snapshots[token] = snapshot{page: result, expires: time.Now().Add(time.Minute), bytes: retainedBytes}
	result.Counters = append([]domain.Counter{}, records[:size]...)
	result.NextPageToken = token + ":" + strconv.Itoa(size)
	return result, nil
}
func (s *Service) continuePage(ctx context.Context, id string, request domain.PageRequest, size int) (domain.Page, error) {
	token, position, ok := strings.Cut(request.PageToken, ":")
	offset, err := strconv.Atoi(position)
	if !ok || err != nil || offset <= 0 {
		return domain.Page{}, domain.ErrInvalidArgument
	}
	generation, err := s.generation(ctx, id)
	if err != nil {
		return domain.Page{}, err
	}
	s.snapshotsMu.Lock()
	defer s.snapshotsMu.Unlock()
	saved, ok := s.snapshots[token]
	if !ok || time.Now().After(saved.expires) {
		delete(s.snapshots, token)
		return domain.Page{}, domain.ErrContinuation
	}
	if saved.page.InstanceID != id || saved.page.Scope != request.Scope {
		return domain.Page{}, domain.ErrContinuation
	}
	if generation != saved.page.Generation {
		delete(s.snapshots, token)
		return domain.Page{}, domain.ErrContinuation
	}
	if offset >= len(saved.page.Counters) {
		return domain.Page{}, domain.ErrInvalidArgument
	}
	result := saved.page
	end := min(offset+size, len(result.Counters))
	result.Counters = append([]domain.Counter{}, result.Counters[offset:end]...)
	if end < len(saved.page.Counters) {
		result.NextPageToken = token + ":" + strconv.Itoa(end)
	}
	return result, ctx.Err()
}
func (s *Service) ReadPair(ctx context.Context, id string, scope domain.Scope, name string) (domain.Pair, error) {
	if err := ctx.Err(); err != nil {
		return domain.Pair{}, err
	}
	started := time.Now()
	if instancevalidators.ID(id) != nil || validators.Selection(domain.Selection{Scope: scope, Name: name, Direction: domain.Uplink}) != nil {
		return domain.Pair{}, domain.ErrInvalidArgument
	}
	selected, ok := s.entries[id]
	if !ok {
		return domain.Pair{}, domain.ErrNotFound
	}
	if selected.reader == nil {
		return domain.Pair{}, domain.ErrUnsupported
	}
	before, err := s.generation(ctx, id)
	if err != nil {
		return domain.Pair{}, err
	}
	result := domain.Pair{InstanceID: id, Scope: scope, Name: name, Generation: before}
	for _, direction := range []domain.Direction{domain.Uplink, domain.Downlink} {
		got, absent, err := s.read(ctx, id, domain.Selection{Scope: scope, Name: name, Direction: direction}, selected)
		if err != nil && !absent {
			return domain.Pair{}, err
		}
		if result.Generation != got.Generation {
			return domain.Pair{}, domain.ErrExecutionChanged
		}
		if absent {
			continue
		}
		if direction == domain.Uplink {
			result.Uplink = &got
		} else {
			result.Downlink = &got
		}
	}
	after, err := s.generation(ctx, id)
	if err != nil {
		return domain.Pair{}, err
	}
	if before != after {
		return domain.Pair{}, domain.ErrExecutionChanged
	}
	result.Source = domain.Source
	result.ObservedAt = time.Now().UTC()
	result.Duration = time.Since(started)
	return result, ctx.Err()
}
