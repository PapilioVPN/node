package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
)

type retained struct {
	page domain.Page
	size int
}
type pageStore struct {
	mu      sync.Mutex
	items   map[string]retained
	pending int
}

func (s *Service) expire(now time.Time) {
	for id, v := range s.pages.items {
		if !now.Before(v.page.ExpiresAt) {
			delete(s.pages.items, id)
		}
	}
}
func (s *Service) List(ctx context.Context, r domain.PageRequest) (domain.Page, error) {
	if validators.ID(r.InstanceID) != nil || r.PageSize < 0 || r.PageSize > domain.MaxPageSize || len(r.PageToken) > 64 {
		return domain.Page{}, domain.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	if r.PageSize == 0 {
		r.PageSize = domain.MaxPageSize
	}
	if r.PageToken != "" {
		return s.continuePage(ctx, r)
	}
	s.pages.mu.Lock()
	s.expire(time.Now())
	if len(s.pages.items)+s.pages.pending >= domain.MaxObservations {
		s.pages.mu.Unlock()
		return domain.Page{}, domain.ErrCapacity
	}
	s.pages.pending++
	s.pages.mu.Unlock()
	defer func() { s.pages.mu.Lock(); s.pages.pending--; s.pages.mu.Unlock() }()
	e, g, done, err := s.begin(ctx, r.InstanceID, false)
	if err != nil {
		return domain.Page{}, err
	}
	defer done()
	started := time.Now()
	rows, err := e.backend.ListRules(ctx)
	if err != nil {
		return domain.Page{}, err
	}
	if len(rows) > domain.MaxRecords {
		return domain.Page{}, domain.ErrCapacity
	}
	bytes := 0
	for _, row := range rows {
		bytes += len(row.OutboundTag) + len(row.RuleTag) + 32
		if bytes > domain.MaxObservationBytes {
			return domain.Page{}, domain.ErrCapacity
		}
	}
	if err := s.stable(ctx, r.InstanceID, g); err != nil {
		return domain.Page{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return domain.Page{}, err
	}
	now := time.Now().UTC()
	v := retained{page: domain.Page{Observation: domain.Observation{InstanceID: strings.Clone(r.InstanceID), Generation: g, ObservedAt: now, Duration: time.Since(started)}, Rules: rows, Total: len(rows), ObservationID: hex.EncodeToString(random[:]), ExpiresAt: now.Add(domain.ObservationTTL)}, size: r.PageSize}
	if len(rows) > r.PageSize {
		s.pages.mu.Lock()
		s.pages.items[v.page.ObservationID] = v
		s.pages.mu.Unlock()
	}
	return pageSlice(v, 0, true), nil
}
func (s *Service) continuePage(ctx context.Context, r domain.PageRequest) (domain.Page, error) {
	id, index, ok := strings.Cut(r.PageToken, ":")
	offset, err := strconv.Atoi(index)
	if !ok || len(id) != 32 || err != nil || offset <= 0 || strconv.Itoa(offset) != index {
		return domain.Page{}, domain.ErrInvalidArgument
	}
	s.pages.mu.Lock()
	s.expire(time.Now())
	v, exists := s.pages.items[id]
	s.pages.mu.Unlock()
	if !exists {
		return domain.Page{}, domain.ErrObservationExpired
	}
	if v.size != r.PageSize || v.page.InstanceID != r.InstanceID || offset >= v.page.Total || offset%v.size != 0 {
		return domain.Page{}, domain.ErrInvalidArgument
	}
	// A continuation is the retained observation, never a repeated full read.
	// An uncertain current execution cannot make that observation current again.
	if err := s.stable(ctx, r.InstanceID, v.page.Generation); err != nil {
		return domain.Page{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	if !time.Now().Before(v.page.ExpiresAt) {
		return domain.Page{}, domain.ErrObservationExpired
	}
	return pageSlice(v, offset, false), nil
}
func pageSlice(v retained, offset int, fresh bool) domain.Page {
	p := v.page
	end := min(offset+v.size, len(p.Rules))
	p.Rules = append([]domain.Rule{}, p.Rules[offset:end]...)
	p.ServedAt = time.Now().UTC()
	p.Fresh = fresh
	if end < p.Total {
		p.NextPageToken = p.ObservationID + ":" + strconv.Itoa(end)
	}
	return p
}
