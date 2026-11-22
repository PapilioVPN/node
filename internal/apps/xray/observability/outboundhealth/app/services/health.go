// Package services coordinates bounded outbound observations on one execution.
package services

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

// Reader validates fresh native records, including tag shape and response bounds.
type Reader interface {
	Read(context.Context) ([]domain.Record, error)
}
type StatusReader interface {
	Status(context.Context, string) (instances.Status, error)
}
type Binding struct {
	ID     string
	Reader Reader
}
type entry struct {
	reader Reader
	slots  chan struct{}
}
type Service struct {
	entries   map[string]entry
	status    StatusReader
	slots     chan struct{}
	mu        sync.Mutex
	snapshots map[string]snapshot
	now       func() time.Time
}

func New(bindings []Binding, status StatusReader) (*Service, error) {
	if status == nil {
		return nil, domain.ErrInvalidArgument
	}
	s := &Service{entries: make(map[string]entry, len(bindings)), status: status, slots: make(chan struct{}, 32), snapshots: make(map[string]snapshot), now: time.Now}
	for _, b := range bindings {
		if instancevalidators.ID(b.ID) != nil {
			return nil, domain.ErrInvalidArgument
		}
		if _, ok := s.entries[b.ID]; ok {
			return nil, domain.ErrInvalidArgument
		}
		s.entries[b.ID] = entry{reader: b.Reader, slots: make(chan struct{}, 8)}
	}
	return s, nil
}
func (s *Service) generation(ctx context.Context, id string) (uint64, error) {
	got, err := s.status.Status(ctx, id)
	if err != nil {
		return 0, err
	}
	if got.Execution.State != instances.Running {
		return 0, domain.ErrNotRunning
	}
	return got.Execution.Generation, nil
}
func (s *Service) read(ctx context.Context, id string) (records []domain.Record, generation uint64, at time.Time, duration time.Duration, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	e, ok := s.entries[id]
	if !ok {
		err = domain.ErrNotFound
		return
	}
	if e.reader == nil {
		err = domain.ErrDisabled
		return
	}
	select {
	case s.slots <- struct{}{}:
	default:
		err = domain.ErrCapacity
		return
	}
	defer func() { <-s.slots }()
	select {
	case e.slots <- struct{}{}:
	default:
		err = domain.ErrCapacity
		return
	}
	defer func() { <-e.slots }()
	start := time.Now()
	generation, err = s.generation(ctx, id)
	if err != nil {
		return
	}
	records, err = e.reader.Read(ctx)
	after, statusErr := s.generation(ctx, id)
	if statusErr != nil {
		err = errors.Join(err, statusErr)
		records = nil
		return
	}
	if after != generation {
		err = errors.Join(err, domain.ErrExecutionChanged)
		records = nil
		return
	}
	if err = errors.Join(err, ctx.Err()); err != nil {
		records = nil
		return
	}
	if len(records) > domain.MaxRecords {
		records = nil
		err = domain.ErrCapacity
		return
	}
	at = s.now().UTC()
	duration = time.Since(start)
	return
}
func (s *Service) Get(ctx context.Context, id string, selection domain.Selection) (domain.Observation, error) {
	age, err := validators.MaxAge(selection.MaxSampleAgeSeconds)
	if err != nil || instancevalidators.ID(id) != nil || validators.Tag(selection.Tag) != nil {
		return domain.Observation{}, domain.ErrInvalidArgument
	}
	records, generation, at, duration, err := s.read(ctx, id)
	if err != nil {
		return domain.Observation{}, err
	}
	result := domain.Observation{InstanceID: id, Generation: generation, Source: domain.Source, ObservedAt: at, Duration: duration, MaxSampleAgeSeconds: age}
	for _, r := range records {
		if r.Tag == selection.Tag {
			if result.Status != nil {
				return domain.Observation{}, domain.ErrUnavailable
			}
			value := present(r, at, age)
			result.Status = &value
		}
	}
	if err := ctx.Err(); err != nil {
		return domain.Observation{}, err
	}
	return result, nil
}
func present(r domain.Record, at time.Time, maxAge uint32) domain.Status {
	v := domain.Status{Record: r, Mode: domain.Ordinary, Freshness: "unknown"}
	if r.Alive && r.DelayMS >= 0 {
		delay := r.DelayMS
		v.LatencyMS = &delay
	}
	if r.HealthPing != nil {
		h := *r.HealthPing
		v.HealthPing = &h
		v.Mode = domain.Burst
		return v
	}
	if r.LastTryUnixSec > 0 && r.LastTryUnixSec <= 253402300799 {
		sample := time.Unix(r.LastTryUnixSec, 0).UTC()
		v.SampledAt = &sample
		if r.LastTryUnixSec <= at.Unix() {
			age := at.Unix() - r.LastTryUnixSec
			v.SampleAgeSeconds = &age
			v.Freshness = "fresh"
			if age > int64(maxAge) {
				v.Freshness = "stale"
			}
		}
	}
	return v
}
