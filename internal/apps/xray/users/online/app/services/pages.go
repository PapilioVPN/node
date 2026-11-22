package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
)

type CollectionReader interface {
	Names(context.Context) ([]domain.Record, error)
	Records(context.Context) ([]domain.Record, error)
}

type pageKind uint8

const (
	namesPage pageKind = iota
	recordsPage
	ipsPage
)

type retained struct {
	page domain.Page
	kind pageKind
	size int
}
type pageStore struct {
	mu           sync.Mutex
	observations map[string]*retained
	pending      int
}

func (s *Service) List(ctx context.Context, r domain.PageRequest) (domain.Page, error) {
	return s.page(ctx, r, namesPage)
}
func (s *Service) Records(ctx context.Context, r domain.PageRequest) (domain.Page, error) {
	return s.page(ctx, r, recordsPage)
}
func (s *Service) PageIPs(ctx context.Context, r domain.PageRequest) (domain.Page, error) {
	return s.page(ctx, r, ipsPage)
}
func (s *Service) page(ctx context.Context, r domain.PageRequest, kind pageKind) (domain.Page, error) {
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	if r.PageSize < 0 || r.PageSize > domain.MaxPageSize || len(r.PageToken) > 64 {
		return domain.Page{}, domain.ErrInvalidArgument
	}
	if kind == ipsPage {
		if err := validateSelection(r.Selection); err != nil {
			return domain.Page{}, err
		}
	} else if instancevalidators.ID(r.InstanceID) != nil || r.Email != "" {
		return domain.Page{}, domain.ErrInvalidArgument
	}
	if r.PageSize == 0 {
		r.PageSize = domain.MaxPageSize
	}
	if r.PageToken != "" {
		return s.continuePage(ctx, r, kind)
	}
	selected, ok := s.entries[r.InstanceID]
	if !ok {
		return domain.Page{}, domain.ErrNotFound
	}
	if selected.reader == nil {
		return domain.Page{}, domain.ErrNotConfigured
	}
	if kind != ipsPage && selected.collection == nil {
		return domain.Page{}, domain.ErrUnsupported
	}
	// Reserve retention before I/O, so concurrent slow first pages cannot bypass
	// the combined pending/retained capacity. No lock spans native work.
	s.pages.mu.Lock()
	s.expire(time.Now())
	if len(s.pages.observations)+s.pages.pending >= domain.MaxObservations {
		s.pages.mu.Unlock()
		return domain.Page{}, domain.ErrCapacity
	}
	s.pages.pending++
	s.pages.mu.Unlock()
	defer func() { s.pages.mu.Lock(); s.pages.pending--; s.pages.mu.Unlock() }()
	var rows []domain.Record
	var state domain.State
	o, err := s.observeRead(ctx, r.Selection, func(reader Reader) error {
		var err error
		switch kind {
		case namesPage:
			rows, err = selected.collection.Names(ctx)
		case recordsPage:
			rows, err = selected.collection.Records(ctx)
		case ipsPage:
			var visitors domain.Visitors
			visitors, err = reader.IPs(ctx, r.Email)
			state = visitors.State()
			rows = make([]domain.Record, len(visitors.IPs))
			email := strings.Clone(r.Email)
			for i := range visitors.IPs {
				rows[i] = domain.Record{Email: email, IP: &visitors.IPs[i]}
			}
		}
		return err
	})
	if err != nil {
		return domain.Page{}, err
	}
	if len(rows) > domain.MaxRecords {
		return domain.Page{}, domain.ErrCapacity
	}
	bytes := 0
	for _, row := range rows {
		bytes += len(row.Email) + 64
		if row.IP != nil {
			bytes += len(row.IP.Address) + 32
		}
		if bytes > domain.MaxObservationBytes {
			return domain.Page{}, domain.ErrCapacity
		}
	}
	// Retain only owned selectors; Fiber may recycle its request storage.
	o.InstanceID = strings.Clone(o.InstanceID)
	o.Email = strings.Clone(o.Email)
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return domain.Page{}, domain.ErrUnavailable
	}
	source := "xray.stats.GetAllOnlineUsers"
	if kind == recordsPage {
		source = "xray.stats.GetUsersStats"
	}
	if kind == ipsPage {
		source = domain.Source
	}
	p := domain.Page{Observation: o, Records: rows, State: state, Source: source, ObservationID: hex.EncodeToString(idBytes), ExpiresAt: o.ObservedAt.Add(domain.ObservationTTL), Total: len(rows)}
	v := &retained{page: p, kind: kind, size: r.PageSize}
	if len(rows) > r.PageSize {
		s.pages.mu.Lock()
		s.pages.observations[p.ObservationID] = v
		s.pages.mu.Unlock()
	}
	return slicePage(v, 0, true), nil
}

// expire is called under the store lock; there is no background lifetime.
func (s *Service) expire(now time.Time) {
	for id, v := range s.pages.observations {
		if !now.Before(v.page.ExpiresAt) {
			delete(s.pages.observations, id)
		}
	}
}
func (s *Service) continuePage(ctx context.Context, r domain.PageRequest, kind pageKind) (domain.Page, error) {
	id, position, ok := strings.Cut(r.PageToken, ":")
	offset, err := strconv.Atoi(position)
	if !ok || len(id) != 32 || err != nil || offset <= 0 || strconv.Itoa(offset) != position {
		return domain.Page{}, domain.ErrInvalidArgument
	}
	s.pages.mu.Lock()
	s.expire(time.Now())
	v := s.pages.observations[id]
	if v == nil {
		s.pages.mu.Unlock()
		return domain.Page{}, domain.ErrObservationExpired
	}
	if v.kind != kind || v.size != r.PageSize || v.page.InstanceID != r.InstanceID || v.page.Email != r.Email || offset >= v.page.Total || offset%v.size != 0 {
		s.pages.mu.Unlock()
		return domain.Page{}, domain.ErrInvalidArgument
	}
	result := slicePage(v, offset, false)
	s.pages.mu.Unlock()
	status, err := s.status.Status(ctx, r.InstanceID)
	if err != nil {
		return domain.Page{}, err
	}
	if status.Execution.Generation != result.Generation {
		return domain.Page{}, domain.ErrExecutionChanged
	}
	if status.Execution.State != instances.Running {
		return domain.Page{}, domain.ErrNotRunning
	}
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	if !time.Now().Before(result.ExpiresAt) {
		return domain.Page{}, domain.ErrObservationExpired
	}
	return result, nil
}
func slicePage(v *retained, offset int, fresh bool) domain.Page {
	p := v.page
	end := min(offset+v.size, len(p.Records))
	// Copy the bounded page and IP values so internal callers cannot mutate the
	// retained observation. Strings from native protobuf are already owned.
	p.Records = append([]domain.Record{}, p.Records[offset:end]...)
	for i := range p.Records {
		if p.Records[i].IP != nil {
			ip := *p.Records[i].IP
			p.Records[i].IP = &ip
		}
	}
	p.ServedAt = time.Now().UTC()
	p.Fresh = fresh
	if end < p.Total {
		p.NextPageToken = p.ObservationID + ":" + strconv.Itoa(end)
	}
	return p
}
