package services

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/policies"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

// Apply consumes the reviewed candidate once. It never recompiles request bytes,
// substitutes a newer revision, retries a mutation or silently falls back from
// live to restart. Formed failures preserve all independent phase evidence.
func (s *Plans) Apply(ctx context.Context, id, planID string) (domain.Application, error) {
	if !validID(planID) {
		return domain.Application{}, domain.ErrInvalidArgument
	}
	entry, err := s.selected(ctx, id)
	if err != nil {
		return domain.Application{}, err
	}
	if old, err := s.Application(ctx, id, planID); err == nil {
		return old, nil
	} else if !errors.Is(err, domain.ErrNoApplication) {
		return domain.Application{}, err
	}
	ctx, _, release, err := s.state.begin(ctx, id)
	if err != nil {
		return domain.Application{}, err
	}
	defer release()
	s.mu.Lock()
	saved := entry.pending
	if saved == nil || saved.plan.ID != planID || time.Now().After(saved.expires) {
		if saved != nil && time.Now().After(saved.expires) {
			entry.pending = nil
		}
		s.mu.Unlock()
		return domain.Application{}, domain.ErrPlanExpired
	}
	entry.pending = nil
	s.mu.Unlock()
	plan := saved.plan
	app := domain.Application{ID: plan.ID, InstanceID: plan.InstanceID, Epoch: s.epoch, ExpectedRevision: plan.ExpectedRevision, Revision: 0, PreviousRevision: plan.ExpectedRevision, CandidateSHA256: plan.CandidateSHA256, Mode: plan.Mode, Outcome: "in_progress", Phase: "preflight", EngineEffect: "not_attempted", Readiness: "not_checked", ExecutionID: plan.ExecutionID, Generation: plan.Generation, StartedAt: time.Now().UTC()}
	s.publish(entry, app)
	metadata, err := entry.binding.Repository.Metadata(ctx)
	if err == nil && metadata.Revision != plan.ExpectedRevision {
		err = domain.ErrStalePlan
	}
	if err == nil && plan.ExpectedRevision != 0 && metadata.SHA256 != plan.PreviousSHA256 {
		err = domain.ErrStalePlan
	}
	if err == nil && plan.ExpectedRevision == 0 {
		var previous domain.Desired
		previous, err = entry.binding.Baseline.Read(ctx)
		if err == nil && policies.SourceDigest(previous.Sources) != plan.PreviousSHA256 {
			err = domain.ErrStalePlan
		}
	}
	if err == nil {
		var state instances.Status
		state, err = s.runtime.Status(ctx, id)
		if err == nil && (state.Execution.Generation != plan.Generation || state.Execution.ExecutionID != plan.ExecutionID || (plan.Mode != domain.Restart && state.Execution.State != instances.Running)) {
			err = domain.ErrStalePlan
		}
	}
	if err != nil {
		return s.finish(entry, app, err, false)
	}
	if err = ctx.Err(); err != nil {
		return s.finish(entry, app, err, false)
	}
	app.Phase = "publication"
	app.RecoveryAvailable = true
	app.RequiresRecovery = true
	s.publish(entry, app)
	app.Persistence, err = entry.binding.Repository.WriteReviewed(ctx, plan.ExpectedRevision, saved.sources, saved.previous, app)
	app.Revision = app.Persistence.Metadata.Revision
	if err != nil {
		app.RequiresRecovery = app.Persistence.Published || app.Persistence.PublicationUnknown
		return s.finish(entry, app, err, false)
	}
	app.Revision = app.Persistence.Metadata.Revision
	app.Phase = "engine"
	s.publish(entry, app)
	if err = ctx.Err(); err != nil {
		return s.finish(entry, app, err, true)
	}
	switch plan.Mode {
	case domain.Unchanged:
		app.EngineEffect = "not_required"
		state, statusErr := s.runtime.Status(context.WithoutCancel(ctx), id)
		if statusErr != nil || state.Execution.Generation != plan.Generation || state.Execution.ExecutionID != plan.ExecutionID || state.Execution.State != instances.Running {
			err = errors.Join(domain.ErrStalePlan, statusErr)
		}
	case domain.LiveRouting:
		app.EngineEffect = "in_progress"
		s.publish(entry, app)
		native, nativeErr := entry.binding.Live.ApplyRouting(ctx, saved.payload)
		app.EngineEffect, app.Acknowledged = native.Effect, native.Acknowledged
		err = nativeErr
		if err == nil && (!native.Acknowledged || native.Effect != "completed") {
			app.EngineEffect = "unknown"
			err = errors.New("unconfirmed configuration mutation")
		}
		state, statusErr := s.runtime.Status(context.WithoutCancel(ctx), id)
		if statusErr != nil || state.Execution.Generation != plan.Generation || state.Execution.ExecutionID != plan.ExecutionID || state.Execution.State != instances.Running {
			if app.EngineEffect != "not_applied" {
				app.EngineEffect = "unknown"
			}
			err = errors.Join(err, domain.ErrStalePlan, statusErr)
		}
	case domain.Restart:
		app.EngineEffect = "in_progress"
		s.publish(entry, app)
		control, controlErr := s.runtime.Restart(ctx, id)
		app.EngineEffect = string(control.Effect)
		app.Acknowledged = control.Effect == instances.Completed
		err = executionError(controlErr)
		if control.Status != nil {
			app.ExecutionID = control.Status.Execution.ExecutionID
			app.Generation = control.Status.Execution.Generation
		}
		if err == nil && (!app.Acknowledged || control.Status == nil) {
			app.EngineEffect = "unknown"
			err = errors.New("unconfirmed configuration execution")
		}
	default:
		err = domain.ErrUnsupported
		app.EngineEffect = "not_applied"
	}
	if err == nil && plan.Mode != domain.Unchanged {
		app.Phase = "readiness"
		s.publish(entry, app)
		app.Readiness, err = s.waitReady(ctx, id, app.ExecutionID, app.Generation)
	}
	return s.finish(entry, app, err, true)
}

func (s *Plans) waitReady(ctx context.Context, id, executionID string, generation uint64) (string, error) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := s.runtime.Check(ctx, id)
		if ready.Status.Execution.Generation != generation || ready.Status.Execution.ExecutionID != executionID {
			return "unavailable", errors.Join(domain.ErrStalePlan, err)
		}
		if ready.Status.Execution.State != instances.Running {
			return "unavailable", errors.Join(domain.ErrExecutionFailed, domain.ErrReadiness, err)
		}
		if ready.APIState == instances.ProbeNotConfigured {
			return "not_configured", domain.ErrReadiness
		}
		if ready.APIState == instances.ProbeReady && err == nil {
			return "api_ready", nil
		}
		if ctx.Err() != nil {
			return "unavailable", errors.Join(domain.ErrReadiness, ctx.Err(), err)
		}
		select {
		case <-ctx.Done():
			return "unavailable", errors.Join(domain.ErrReadiness, ctx.Err(), err)
		case <-ticker.C:
		}
	}
}
func (s *Plans) finish(entry *plannedEntry, app domain.Application, cause error, persist bool) (domain.Application, error) {
	now := time.Now().UTC()
	app.CompletedAt = &now
	app.Error = applicationError(cause)
	app.Phase = "finished"
	switch {
	case cause != nil && app.EngineEffect == "unknown":
		app.Outcome = "unknown"
	case errors.Is(cause, domain.ErrExecutionFailed):
		app.Outcome = "failed"
	case cause != nil && app.Acknowledged:
		app.Outcome = "applied_unverified"
	case cause != nil:
		app.Outcome = "failed"
	case app.Mode == domain.Unchanged:
		app.Outcome = "unchanged"
		app.RequiresRecovery = false
	default:
		app.Outcome = "completed"
		app.RequiresRecovery = false
	}
	if persist {
		// Final receipt persistence has its own short completion budget, survives a
		// canceled reply, and remains joined by StateService's active-operation owner.
		completion, cancel := context.WithTimeout(context.Background(), time.Second)
		saveErr := entry.binding.Repository.SaveApplication(completion, app.Revision, app.ID, app)
		cancel()
		app.ReceiptDurable = saveErr == nil
		if saveErr != nil {
			app.RequiresRecovery = true
			app.Outcome = "recovery_required"
			app.Error = "receipt_durability_unknown"
			cause = errors.Join(cause, domain.ErrDurabilityUnknown, saveErr)
		}
	}
	s.publish(entry, app)
	return cloneApplication(app), cause
}
