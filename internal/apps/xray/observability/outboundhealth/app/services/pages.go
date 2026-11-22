package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
)

const snapshotTTL = 30 * time.Second
const maxSnapshots = 8
const maxSnapshotBytes = 8 << 20

type snapshot struct {
	page    domain.Page
	expires time.Time
	bytes   int
}

// List reads natively once for the first page. A new paged read for an instance
// replaces its previous continuation. Retention is volatile, 30 seconds, at most
// eight observations/8 MiB total. Continuations never refresh native evidence.
func (s *Service) List(ctx context.Context, id string, request domain.PageRequest) (domain.Page, error) {
	age, err := validators.MaxAge(request.MaxSampleAgeSeconds)
	if err != nil || instancevalidators.ID(id) != nil || request.PageSize > domain.MaxPageSize || len(request.PageToken) > 96 {
		return domain.Page{}, domain.ErrInvalidArgument
	}
	size := int(request.PageSize)
	if size == 0 {
		size = domain.MaxPageSize
	}
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	if _, ok := s.entries[id]; !ok {
		return domain.Page{}, domain.ErrNotFound
	}
	if request.PageToken != "" {
		return s.continuePage(ctx, id, request.PageToken, size, age)
	}
	records, generation, at, duration, err := s.read(ctx, id)
	if err != nil {
		return domain.Page{}, err
	}
	// The adapter owns native bounds; account for retained Go data separately.
	bytes := 0
	for _, r := range records {
		bytes += len(r.Tag) + 256
	}
	if bytes > maxSnapshotBytes {
		return domain.Page{}, domain.ErrCapacity
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].Tag < records[j].Tag })
	p := domain.Page{InstanceID: strings.Clone(id), Generation: generation, Source: domain.Source, ObservedAt: at, Duration: duration, MaxSampleAgeSeconds: age, Statuses: make([]domain.Status, 0, len(records)), ServedAt: at, Total: uint32(len(records))}
	for _, r := range records {
		r.Tag = strings.Clone(r.Tag)
		p.Statuses = append(p.Statuses, present(r, at, age))
	}
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	used, retained := 0, 0
	var superseded []string
	for token, old := range s.snapshots {
		if !now.Before(old.expires) {
			delete(s.snapshots, token)
			continue
		}
		if old.page.InstanceID == id {
			superseded = append(superseded, token)
			continue
		}
		used += old.bytes
		retained++
	}
	if len(records) <= size {
		for _, token := range superseded {
			delete(s.snapshots, token)
		}
		return p, nil
	}
	if retained >= maxSnapshots || used+bytes > maxSnapshotBytes {
		return domain.Page{}, domain.ErrCapacity
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return domain.Page{}, err
	}
	for _, oldToken := range superseded {
		delete(s.snapshots, oldToken)
	}
	token := hex.EncodeToString(random[:])
	expires := now.Add(snapshotTTL)
	p.ExpiresAt = &expires
	s.snapshots[token] = snapshot{page: p, expires: expires, bytes: bytes}
	return slicePage(p, token, 0, size, now), nil
}
func pageSlice(p domain.Page, offset, size int, servedAt time.Time) domain.Page {
	p.ServedAt = servedAt.UTC()
	if p.ExpiresAt != nil {
		expires := *p.ExpiresAt
		p.ExpiresAt = &expires
	}
	end := min(offset+size, len(p.Statuses))
	p.Statuses = append([]domain.Status{}, p.Statuses[offset:end]...)
	// Own all pointer-bearing records returned to internal callers.
	for i := range p.Statuses {
		p.Statuses[i] = present(p.Statuses[i].Record, p.ServedAt, p.MaxSampleAgeSeconds)
	}
	p.NextPageToken = ""
	return p
}
func (s *Service) continuePage(ctx context.Context, id, token string, size int, age uint32) (domain.Page, error) {
	key, position, ok := strings.Cut(token, ":")
	offset, err := strconv.Atoi(position)
	if !ok || len(key) != 32 || err != nil || offset <= 0 {
		return domain.Page{}, domain.ErrInvalidArgument
	}
	generation, err := s.generation(ctx, id)
	if err != nil {
		return domain.Page{}, err
	}
	s.mu.Lock()
	saved, ok := s.snapshots[key]
	if !ok || !s.now().Before(saved.expires) {
		delete(s.snapshots, key)
		s.mu.Unlock()
		return domain.Page{}, domain.ErrContinuation
	}
	if saved.page.InstanceID != id || saved.page.MaxSampleAgeSeconds != age {
		s.mu.Unlock()
		return domain.Page{}, domain.ErrContinuation
	}
	if saved.page.Generation != generation {
		delete(s.snapshots, key)
		s.mu.Unlock()
		return domain.Page{}, domain.ErrContinuation
	}
	if offset >= len(saved.page.Statuses) {
		s.mu.Unlock()
		return domain.Page{}, domain.ErrInvalidArgument
	}
	p := slicePage(saved.page, key, offset, size, s.now())
	s.mu.Unlock()
	after, err := s.generation(ctx, id)
	if err != nil {
		return domain.Page{}, err
	}
	if after != generation {
		return domain.Page{}, domain.ErrContinuation
	}
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	return p, nil
}
func slicePage(p domain.Page, token string, offset, size int, servedAt time.Time) domain.Page {
	total := len(p.Statuses)
	result := pageSlice(p, offset, size, servedAt)
	if end := min(offset+size, total); end < total {
		result.NextPageToken = token + ":" + strconv.Itoa(end)
	}
	return result
}
