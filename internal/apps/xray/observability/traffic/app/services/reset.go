package services

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
)

// Resetter receives a validated exact selection. Discovery results are checked
// by their reader before the service derives exact reset selections.
type Resetter interface {
	Reset(context.Context, domain.Selection) (int64, error)
}
type control struct {
	mu               sync.Mutex
	revision         uint64
	request          domain.ResetRequest
	report           domain.ResetReport
	active           bool
	fencedGeneration uint64
}

// Reset serializes destructive work per instance, reserving one of eight global
// slots. Scope discovery uses QueryStats(false), followed by ordered exact swaps.
// This safe 1+N fallback is non-atomic; discovery bytes are never reset receipts.
func (s *Service) Reset(ctx context.Context, id string, request domain.ResetRequest) (domain.ResetReport, error) {
	if err := ctx.Err(); err != nil {
		return domain.ResetReport{}, err
	}
	if instancevalidators.ID(id) != nil || validators.Reset(request) != nil {
		return domain.ResetReport{}, domain.ErrInvalidArgument
	}
	selected, ok := s.entries[id]
	if !ok {
		return domain.ResetReport{}, domain.ErrNotFound
	}
	c := selected.control
	c.mu.Lock()
	if request.Epoch != s.epoch {
		c.mu.Unlock()
		return domain.ResetReport{}, domain.ErrConflict
	}
	if c.report.OperationID == request.OperationID {
		if request != c.request {
			c.mu.Unlock()
			return domain.ResetReport{}, domain.ErrConflict
		}
		result := reportPage(c.report, 0)
		c.mu.Unlock()
		return result, nil
	}
	if c.active {
		c.mu.Unlock()
		return domain.ResetReport{}, domain.ErrCapacity
	}
	if request.ExpectedRevision != c.revision || c.revision == math.MaxUint64 {
		c.mu.Unlock()
		return domain.ResetReport{}, domain.ErrConflict
	}
	if selected.resetter == nil || (request.Kind == domain.ResetScope && selected.lister == nil) {
		c.mu.Unlock()
		return domain.ResetReport{}, domain.ErrUnsupported
	}
	// Reserve before releasing the short lock; no lock spans status or native I/O.
	select {
	case s.writes <- struct{}{}:
	default:
		c.mu.Unlock()
		return domain.ResetReport{}, domain.ErrCapacity
	}
	c.active = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.active = false; c.mu.Unlock(); <-s.writes }()
	generation, err := s.generation(ctx, id)
	if err != nil {
		return domain.ResetReport{}, err
	}
	c.mu.Lock()
	if c.fencedGeneration != 0 && c.fencedGeneration == generation {
		c.mu.Unlock()
		return domain.ResetReport{}, domain.ErrRestartRequired
	}
	request.Epoch = strings.Clone(request.Epoch)
	request.OperationID = strings.Clone(request.OperationID)
	request.Selection.Name = strings.Clone(request.Selection.Name)
	c.revision++
	c.request = request
	c.report = domain.ResetReport{InstanceID: strings.Clone(id), Epoch: s.epoch, OperationID: request.OperationID, Revision: c.revision, Generation: generation, Kind: request.Kind, Scope: request.Selection.Scope, Name: request.Selection.Name, Direction: request.Selection.Direction, Outcome: "in_progress", StartedAt: time.Now().UTC(), Items: []domain.ResetItem{}}
	c.mu.Unlock()
	// A formed operation reports operational failures inside its bounded envelope.
	finish := func(cause error) (domain.ResetReport, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		now := time.Now().UTC()
		c.report.CompletedAt = &now
		c.report.Error = errorCode(cause)
		ack, unknown, failed := false, false, cause != nil
		for _, item := range c.report.Items {
			ack = ack || item.Effect == domain.Acknowledged
			unknown = unknown || item.Effect == domain.Unknown
			failed = failed || item.Effect == domain.NotAttempted || item.Effect == domain.NotApplied
		}
		switch {
		case unknown:
			c.report.Outcome = "unknown"
		case failed && ack:
			c.report.Outcome = "partial"
		case failed:
			c.report.Outcome = "not_applied"
		default:
			c.report.Outcome = "completed"
		}
		c.report.RecoveryRequired = unknown
		if unknown {
			c.fencedGeneration = generation
		} else {
			c.fencedGeneration = 0
		}
		return reportPage(c.report, 0), nil
	}
	selections := []domain.Selection{request.Selection}
	if request.Kind == domain.ResetPair {
		selections = []domain.Selection{{Scope: request.Selection.Scope, Name: request.Selection.Name, Direction: domain.Uplink}, {Scope: request.Selection.Scope, Name: request.Selection.Name, Direction: domain.Downlink}}
	}
	if request.Kind == domain.ResetScope {
		records, err := selected.lister.List(ctx, request.Selection.Scope)
		if err != nil {
			return finish(err)
		}
		if len(records) > int(request.MaxCounters) {
			return finish(domain.ErrCapacity)
		}
		selections = make([]domain.Selection, len(records))
		seen := make(map[domain.Selection]bool, len(records))
		for i, record := range records {
			if seen[record.Selection] {
				return finish(domain.ErrUnavailable)
			}
			seen[record.Selection] = true
			selections[i] = record.Selection
		}
	}
	c.mu.Lock()
	c.report.Items = make([]domain.ResetItem, len(selections))
	c.report.Total = uint32(len(selections))
	for i, selection := range selections {
		selection.Name = strings.Clone(selection.Name)
		c.report.Items[i] = domain.ResetItem{Selection: selection, Effect: domain.NotAttempted}
	}
	c.mu.Unlock()
	for i, selection := range selections {
		observed, err := s.generation(ctx, id)
		if err == nil && observed != generation {
			err = domain.ErrExecutionChanged
		}
		if err != nil {
			return finish(err)
		}
		if err := ctx.Err(); err != nil {
			return finish(err)
		}
		c.mu.Lock()
		c.report.Items[i].Attempted = true
		c.report.Items[i].Effect = domain.Unknown
		c.mu.Unlock()
		value, resetErr := selected.resetter.Reset(ctx, selection)
		now := time.Now().UTC()
		// Inspect local status without losing an already received native ACK when the
		// caller deadline races. A changed execution still blocks attribution/replay.
		after, statusErr := s.generation(context.WithoutCancel(ctx), id)
		if statusErr == nil && after != generation {
			statusErr = domain.ErrExecutionChanged
		}
		item := domain.ResetItem{Selection: selection, Attempted: true, Effect: domain.Acknowledged, PreviousBytes: &value, AcknowledgedAt: &now}
		if resetErr != nil {
			item.PreviousBytes = nil
			item.AcknowledgedAt = nil
			item.Error = errorCode(resetErr)
			item.Effect = domain.NotApplied
			if errors.Is(resetErr, domain.ErrResetUnknown) {
				item.Effect = domain.Unknown
			} else if errors.Is(resetErr, domain.ErrCounterNotFound) {
				item.Effect = domain.Missing
			}
		}
		if statusErr != nil {
			item.Effect = domain.Unknown
			item.Error = errorCode(statusErr)
			resetErr = errors.Join(domain.ErrResetUnknown, resetErr, statusErr)
		}
		c.mu.Lock()
		c.report.Items[i] = item
		c.mu.Unlock()
		if errors.Is(resetErr, domain.ErrCounterNotFound) && request.Kind == domain.ResetPair && !errors.Is(resetErr, domain.ErrResetUnknown) {
			continue
		}
		if resetErr != nil {
			return finish(resetErr)
		}
	}
	return finish(nil)
}

// ResetStatus returns the current revision when revision is zero, or a page of
// the exact retained revision. It never performs native I/O or replays a reset.
func (s *Service) ResetStatus(ctx context.Context, id string, revision uint64, offset uint32) (domain.ResetReport, error) {
	if err := ctx.Err(); err != nil {
		return domain.ResetReport{}, err
	}
	if instancevalidators.ID(id) != nil {
		return domain.ResetReport{}, domain.ErrInvalidArgument
	}
	selected, ok := s.entries[id]
	if !ok {
		return domain.ResetReport{}, domain.ErrNotFound
	}
	c := selected.control
	c.mu.Lock()
	defer c.mu.Unlock()
	if revision != 0 && revision != c.revision {
		return domain.ResetReport{}, domain.ErrConflict
	}
	if (offset > 0 && revision == 0) || offset > uint32(len(c.report.Items)) {
		return domain.ResetReport{}, domain.ErrInvalidArgument
	}
	if c.revision == 0 {
		return domain.ResetReport{InstanceID: id, Epoch: s.epoch, Outcome: "idle", Items: []domain.ResetItem{}}, nil
	}
	return reportPage(c.report, offset), nil
}
func reportPage(report domain.ResetReport, offset uint32) domain.ResetReport {
	end := min(int(offset)+domain.MaxPageSize, len(report.Items))
	report.Items = append([]domain.ResetItem{}, report.Items[int(offset):end]...)
	// Clone pointer values too: the caller cannot alter retained reset evidence.
	for i := range report.Items {
		if p := report.Items[i].PreviousBytes; p != nil {
			v := *p
			report.Items[i].PreviousBytes = &v
		}
		if p := report.Items[i].AcknowledgedAt; p != nil {
			v := *p
			report.Items[i].AcknowledgedAt = &v
		}
	}
	if report.CompletedAt != nil {
		v := *report.CompletedAt
		report.CompletedAt = &v
	}
	if end < int(report.Total) {
		report.NextOffset = uint32(end)
	}
	return report
}
func errorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, domain.ErrExecutionChanged):
		return "execution_changed"
	case errors.Is(err, domain.ErrCounterNotFound):
		return "counter_not_found"
	case errors.Is(err, domain.ErrUnsupported):
		return "failed_precondition"
	case errors.Is(err, domain.ErrCapacity):
		return "resource_exhausted"
	case errors.Is(err, domain.ErrNotRunning):
		return "not_running"
	default:
		return "unavailable"
	}
}
