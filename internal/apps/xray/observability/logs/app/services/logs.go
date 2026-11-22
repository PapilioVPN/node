// Package services coordinates bounded console reads without per-consumer queues.
package services

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
)

type Reader interface {
	Read(context.Context, string, uint64, uint32) (domain.Chunk, error)
}
type Binding struct {
	ID     string
	Reader Reader
}
type entry struct {
	reader Reader
	active chan struct{}
}
type Service struct {
	entries map[string]entry
	active  chan struct{}
}

func New(bindings []Binding) (*Service, error) {
	s := &Service{entries: map[string]entry{}, active: make(chan struct{}, domain.MaxConcurrentReads)}
	for _, b := range bindings {
		if validators.ID(b.ID) != nil {
			return nil, domain.ErrInvalidArgument
		}
		if _, ok := s.entries[b.ID]; ok {
			return nil, domain.ErrInvalidArgument
		}
		s.entries[b.ID] = entry{reader: b.Reader, active: make(chan struct{}, domain.MaxInstanceReads)}
	}
	return s, nil
}
func (s *Service) begin(ctx context.Context, id string) (entry, func(), error) {
	if err := ctx.Err(); err != nil {
		return entry{}, nil, err
	}
	if err := validators.ID(id); err != nil {
		return entry{}, nil, err
	}
	e, ok := s.entries[id]
	if !ok {
		return entry{}, nil, domain.ErrNotFound
	}
	if e.reader == nil {
		return entry{}, nil, domain.ErrUnsupported
	}
	select {
	case s.active <- struct{}{}:
	default:
		return entry{}, nil, domain.ErrCapacity
	}
	select {
	case e.active <- struct{}{}:
	default:
		<-s.active
		return entry{}, nil, domain.ErrCapacity
	}
	return e, func() { <-e.active; <-s.active }, nil
}
func read(ctx context.Context, e entry, p position) (domain.Chunk, error) {
	c, err := e.reader.Read(ctx, p.execution, p.offset, domain.MaxReadBytes)
	if err != nil {
		return domain.Chunk{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Chunk{}, err
	}
	if validators.ID(c.ExecutionID) != nil || c.Generation == 0 || c.Offset != p.offset || c.NextOffset < c.Offset || c.NextOffset > domain.MaxOutputBytes || c.NextOffset-c.Offset != uint64(len(c.Data)) || len(c.Data) > domain.MaxReadBytes {
		return domain.Chunk{}, domain.ErrInvalidResponse
	}
	if p.execution != "" && (c.ExecutionID != p.execution || c.Generation != p.generation) {
		return domain.Chunk{}, domain.ErrExecutionChanged
	}
	return c, nil
}
func page(id string, c domain.Chunk) domain.Page {
	p := domain.Page{InstanceID: id, Source: "console", ExecutionID: c.ExecutionID, Generation: c.Generation, ObservedAt: c.ObservedAt, Records: []domain.Record{}, Gaps: []domain.Gap{}, CapacityReached: c.CapacityReached, PositionUncertain: c.PositionUncertain, Reattached: c.Reattached, Exited: c.Exited}
	if c.PositionUncertain {
		p.Gaps = append(p.Gaps, domain.Gap{Reason: "position_uncertain", FromOffset: c.NextOffset})
	}
	return p
}
func capacity(p *domain.Page) {
	if p.CapacityReached {
		p.Gaps = append(p.Gaps, domain.Gap{Reason: "source_capacity_reached", FromOffset: domain.MaxOutputBytes})
	}
}
func (s *Service) Tail(ctx context.Context, id string, r domain.TailRequest) (domain.Page, error) {
	limit, err := validators.Limit(r.Limit)
	if err != nil {
		return domain.Page{}, err
	}
	e, release, err := s.begin(ctx, id)
	if err != nil {
		return domain.Page{}, err
	}
	defer release()
	return tail(ctx, e, id, limit)
}

// Tail scans at most the source's 1 MiB prefix in sixteen bounded reads, retaining
// at most that many raw bytes for framing during this request. Follow reads one
// 64 KiB chunk per poll. Neither keeps raw data or a goroutine between requests.
func tail(ctx context.Context, e entry, id string, limit int) (domain.Page, error) {
	pos := position{instance: id}
	var all []byte
	var last domain.Chunk
	for {
		c, err := read(ctx, e, pos)
		if err != nil {
			return domain.Page{}, err
		}
		last = c
		pos.execution = c.ExecutionID
		pos.generation = c.Generation
		pos.offset = c.NextOffset
		all = append(all, c.Data...)
		if len(c.Data) < domain.MaxReadBytes || pos.offset == domain.MaxOutputBytes {
			break
		}
	}
	p := page(id, last)
	pos.offset = 0
	records, gaps, next := frame(all, pos, last.Exited || last.CapacityReached, limit, true)
	p.Gaps = append(p.Gaps, gaps...)
	p.Records = records
	p.NextCursor = encode(next)
	capacity(&p)
	return p, ctx.Err()
}
func (s *Service) Follow(ctx context.Context, id string, r domain.FollowRequest) (domain.Page, error) {
	limit, err := validators.Limit(r.Limit)
	if err != nil || r.WaitMilliseconds > domain.MaxWaitMilliseconds {
		return domain.Page{}, domain.ErrInvalidArgument
	}
	pos, err := decode(r.Cursor, id)
	if err != nil {
		return domain.Page{}, err
	}
	e, release, err := s.begin(ctx, id)
	if err != nil {
		return domain.Page{}, err
	}
	defer release()
	until := time.Now().Add(time.Duration(r.WaitMilliseconds) * time.Millisecond)
	for {
		c, err := read(ctx, e, pos)
		if errors.Is(err, domain.ErrExecutionChanged) {
			p, err := tail(ctx, e, id, limit)
			if err == nil {
				p.Gaps = append([]domain.Gap{{Reason: "execution_changed", FromOffset: pos.offset, PreviousExecutionID: pos.execution}}, p.Gaps...)
			}
			return p, err
		}
		if err != nil {
			return domain.Page{}, err
		}
		p := page(id, c)
		records, gaps, next := frame(c.Data, pos, c.Exited && len(c.Data) < domain.MaxReadBytes || c.CapacityReached && c.NextOffset == domain.MaxOutputBytes, limit, false)
		p.Records = records
		p.Gaps = append(p.Gaps, gaps...)
		p.NextCursor = encode(next)
		capacity(&p)
		remaining := time.Until(until)
		if len(records) > 0 || len(p.Gaps) > 0 || next.offset != pos.offset || c.Exited || remaining <= 0 {
			return p, ctx.Err()
		}
		timer := time.NewTimer(min(100*time.Millisecond, remaining))
		select {
		case <-ctx.Done():
			timer.Stop()
			return domain.Page{}, ctx.Err()
		case <-timer.C:
		}
	}
}
