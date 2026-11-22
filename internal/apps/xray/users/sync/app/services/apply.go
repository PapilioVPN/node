package services

import (
	"context"
	"errors"
	"time"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/policies"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/ports"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

func (s *Service) Apply(ctx context.Context, id, planID string, expected domain.Revision) (domain.ReceiptPage, error) {
	e, release, err := s.acquire(ctx, id)
	if err != nil {
		return domain.ReceiptPage{}, err
	}
	defer release()
	s.mu.Lock()
	p := e.plan
	if p == nil || p.Preview.PlanID != planID || p.Preview.Revision != expected {
		s.mu.Unlock()
		return domain.ReceiptPage{}, domain.ErrStale
	}
	if e.receipt != nil {
		cause := e.cause
		s.mu.Unlock()
		page, _ := s.Status(context.WithoutCancel(ctx), id, p.Request.OperationID, 0)
		return page, cause
	}
	s.mu.Unlock()
	if !s.now().Before(p.Preview.ExpiresAt) {
		return domain.ReceiptPage{}, domain.ErrStale
	}
	current, err := s.observer.Snapshot(ctx, id, p.Request.Selection)
	if err != nil {
		return domain.ReceiptPage{}, err
	}
	current.Revision.Epoch, current.Revision.Control = s.epoch, e.control
	if current.Revision != expected || !policies.SameObservation(p.Snapshot, current) {
		return domain.ReceiptPage{}, domain.ErrStale
	}
	r := domain.Receipt{InstanceID: id, OperationID: p.Request.OperationID, Revision: expected, Effect: accounts.NotApplied, InFlight: true}
	if p.Request.Mode == domain.Delta {
		for _, step := range p.Steps {
			r.Steps = append(r.Steps, domain.StepResult{InboundTag: step.InboundTag, Email: step.Email, Action: step.Action, Effect: accounts.NotApplied, Code: "not_attempted"})
		}
	}
	s.mu.Lock()
	e.control++
	r.Revision.Control = e.control
	e.receipt = &r
	s.mu.Unlock()
	if p.Request.Mode == domain.Full {
		var result ports.ResultV1
		result, err = s.configs.ApplyMembership(ctx, id, e.full.Token)
		s.mu.Lock()
		applyFullResult(&r, result, err)
		s.mu.Unlock()
	} else {
		err = s.applyLive(ctx, e, p, &r)
	}
	s.mu.Lock()
	r.InFlight = false
	r.CompletedAt = s.now().UTC()
	e.cause = err
	// Keep review identity for exact replay, release all native account material.
	e.plan = &domain.Plan{Preview: p.Preview, Request: domain.Request{OperationID: p.Request.OperationID, Mode: p.Request.Mode}}
	retained := 1024
	for _, step := range r.Steps {
		retained += len(step.InboundTag) + len(step.Email) + 128
	}
	s.bytes += retained - e.bytes
	e.bytes = retained
	s.mu.Unlock()
	page, _ := s.Status(context.WithoutCancel(ctx), id, r.OperationID, 0)
	return page, err
}

func (s *Service) applyLive(ctx context.Context, e *syncEntry, p *domain.Plan, receipt *domain.Receipt) error {
	var cause error
	for start := 0; start < len(p.Steps); {
		if err := ctx.Err(); err != nil {
			cause = err
			break
		}
		// Local facts only: never re-list membership for each mutation group.
		state, err := s.observer.Snapshot(ctx, p.Request.InstanceID, nil)
		if err != nil {
			cause = err
			break
		}
		if state.Revision.Config != p.Preview.Revision.Config || state.Revision.Generation != p.Preview.Revision.Generation {
			cause = domain.ErrStale
			break
		}
		end := start + 1
		for end < len(p.Steps) && end-start < accounts.MaxBatchItems && p.Steps[end].Action == p.Steps[start].Action {
			end++
		}
		s.mu.Lock()
		receipt.Effect = accounts.Unknown
		s.mu.Unlock()
		var batch accounts.Batch
		if p.Steps[start].Action == domain.Remove {
			items := make([]accounts.Removal, end-start)
			for i, step := range p.Steps[start:end] {
				items[i] = accounts.Removal{InboundTag: step.InboundTag, Email: step.Email}
			}
			batch, err = s.accounts.RemoveMany(ctx, p.Request.InstanceID, items, 1)
		} else {
			items := make([]accounts.Addition, end-start)
			for i, step := range p.Steps[start:end] {
				items[i] = accounts.Addition{InboundTag: step.InboundTag, Configuration: step.Configuration}
			}
			batch, err = s.accounts.AddMany(ctx, p.Request.InstanceID, items, 1)
		}
		s.mu.Lock()
		if len(batch.Items) != end-start {
			for i := start; i < end; i++ {
				receipt.Steps[i].Effect = accounts.Unknown
				receipt.Steps[i].Code = "unknown"
			}
			err = errors.Join(err, domain.ErrUncertain)
		} else {
			for i, item := range batch.Items {
				r := &receipt.Steps[start+i]
				r.Attempted, r.Effect, r.Acknowledged, r.RequiresRestart = item.Attempted, item.Operation.Effect, item.Operation.Acknowledged, item.Operation.RequiresInstanceRestart
				r.Code = resultCode(item.Err)
				if r.Effect == "" {
					r.Effect, r.Code = accounts.Unknown, "unknown"
					err = errors.Join(err, domain.ErrUncertain)
				}
			}
		}
		s.mu.Unlock()
		if err != nil {
			cause = err
			break
		}
		start = end
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	completed, unknown := 0, false
	for _, step := range receipt.Steps {
		if step.Effect == accounts.Completed {
			completed++
		}
		unknown = unknown || step.Effect == accounts.Unknown || step.RequiresRestart
	}
	switch {
	case unknown:
		receipt.Effect, receipt.RequiresRecovery = accounts.Unknown, true
	case completed == len(receipt.Steps) && cause == nil:
		receipt.Effect = accounts.Completed
	case completed > 0:
		receipt.Effect = accounts.Partial
	default:
		receipt.Effect = accounts.NotApplied
	}
	return cause
}
func resultCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, accounts.ErrNotAttempted):
		return "not_attempted"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, accounts.ErrCapacity):
		return "capacity_exceeded"
	default:
		return "account_operation_failed"
	}
}
func applyFullResult(r *domain.Receipt, result ports.ResultV1, err error) {
	r.Revision.Config, r.Revision.Generation = result.Revision, result.Generation
	r.Durable, r.Ready, r.RequiresRecovery = result.Durable, result.Ready, result.RequiresRecovery
	r.Effect = result.Effect
	if r.Effect == "" {
		r.Effect, r.RequiresRecovery = accounts.Unknown, true
	}
	if r.Effect == accounts.Completed && (!r.Durable || !r.Ready) {
		r.Effect, r.RequiresRecovery = accounts.Partial, true
	}
	if err != nil && r.Effect == accounts.Unknown {
		r.RequiresRecovery = true
	}
}

// Refresh recovers a full-sync result after response loss without reapplying it.
// Delta uncertainty clears only after explicit execution replacement; the old
// effect stays unknown rather than being relabeled as a successful old write.
func (s *Service) Refresh(ctx context.Context, id, operation string) (domain.ReceiptPage, error) {
	e, release, err := s.acquire(ctx, id)
	if err != nil {
		return domain.ReceiptPage{}, err
	}
	defer release()
	s.mu.Lock()
	valid := e.receipt != nil && e.receipt.OperationID == operation
	s.mu.Unlock()
	if !valid {
		return domain.ReceiptPage{}, domain.ErrNotFound
	}
	if e.plan.Request.Mode == domain.Full {
		result, err := s.configs.MembershipStatus(ctx, id, e.full.Token)
		if err != nil {
			return domain.ReceiptPage{}, err
		}
		s.mu.Lock()
		applyFullResult(e.receipt, result, nil)
		s.mu.Unlock()
	} else {
		current, err := s.observer.Snapshot(ctx, id, nil)
		if err != nil {
			return domain.ReceiptPage{}, err
		}
		s.mu.Lock()
		if current.Revision.Generation != e.receipt.Revision.Generation {
			e.receipt.RequiresRecovery = false
		}
		s.mu.Unlock()
	}
	return s.Status(ctx, id, operation, 0)
}

func (s *Service) Recover(ctx context.Context, id, operation string, expectedConfig uint64) (domain.ReceiptPage, error) {
	e, release, err := s.acquire(ctx, id)
	if err != nil {
		return domain.ReceiptPage{}, err
	}
	defer release()
	s.mu.Lock()
	valid := e.receipt != nil && e.receipt.OperationID == operation && e.plan.Request.Mode == domain.Full
	s.mu.Unlock()
	if !valid {
		return domain.ReceiptPage{}, domain.ErrNotFound
	}
	result, err := s.configs.RecoverMembership(ctx, id, e.full.Token, expectedConfig)
	s.mu.Lock()
	applyFullResult(e.receipt, result, err)
	e.cause = err
	e.receipt.CompletedAt = time.Now().UTC()
	s.mu.Unlock()
	page, _ := s.Status(context.WithoutCancel(ctx), id, operation, 0)
	return page, err
}
