package services

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

// Recover is an explicit CAS restoration, with its own consumed identity. It
// retains original previous sources and advances the desired revision. A lost
// recovery reply is read/replayed by the SAME identity, never another restart.
func (s *Plans) Recover(ctx context.Context, id, applicationID string, expected uint64, recoveryID string) (domain.Application, error) {
	if !validID(applicationID) || !validID(recoveryID) {
		return domain.Application{}, domain.ErrInvalidArgument
	}
	entry, err := s.selected(ctx, id)
	if err != nil {
		return domain.Application{}, err
	}
	old, err := s.Application(ctx, id, applicationID)
	if err != nil {
		return domain.Application{}, err
	}
	if old.Recovery != nil {
		if old.Recovery.ID != recoveryID || old.Recovery.ExpectedRevision != expected {
			return domain.Application{}, domain.ErrConflict
		}
		return old, nil
	}
	ctx, _, release, err := s.state.begin(ctx, id)
	if err != nil {
		return domain.Application{}, err
	}
	defer release()
	if old.Revision != expected || !old.RequiresRecovery || !old.RecoveryAvailable {
		return domain.Application{}, domain.ErrConflict
	}
	if _, err := entry.binding.Repository.Confirm(ctx, expected); err != nil {
		return domain.Application{}, err
	}
	saved, persistence, err := entry.binding.Repository.WriteRecovery(ctx, expected, applicationID, recoveryID)
	if err != nil {
		if saved.Application.ID == "" {
			return domain.Application{}, err
		}
		app := saved.Application
		app.Recovery.Persistence = persistence
		app.Recovery.Error = applicationError(err)
		app.Recovery.Outcome = "recovery_required"
		app.RequiresRecovery = true
		s.publish(entry, app)
		return cloneApplication(app), err
	}
	app := saved.Application
	if app.Outcome == "in_progress" {
		app.Outcome, app.EngineEffect, app.Phase, app.Error = "unknown", "unknown", "interrupted", "operation_interrupted"
	}
	app.Recovery.Persistence = persistence
	s.publish(entry, app)
	recovery := app.Recovery
	if persistence.Durable && ctx.Err() == nil {
		recovery.EngineEffect = "in_progress"
		s.publish(entry, app)
		control, controlErr := s.runtime.Restart(ctx, id)
		recovery.EngineEffect = string(control.Effect)
		err = executionError(controlErr)
		if control.Status != nil {
			recovery.ExecutionID = control.Status.Execution.ExecutionID
			recovery.Generation = control.Status.Execution.Generation
		}
		if err == nil && (control.Effect != instances.Completed || control.Status == nil) {
			recovery.EngineEffect = "unknown"
			err = errors.New("unconfirmed recovery execution")
		}
		if err == nil {
			recovery.Readiness, err = s.waitReady(ctx, id, recovery.ExecutionID, recovery.Generation)
		}
	} else {
		err = errors.Join(ctx.Err(), domain.ErrDurabilityUnknown)
	}
	now := time.Now().UTC()
	recovery.CompletedAt = &now
	recovery.Error = applicationError(err)
	recovery.Outcome = "failed"
	if recovery.EngineEffect == "unknown" {
		recovery.Outcome = "unknown"
	}
	if err == nil {
		recovery.Outcome = "completed"
		app.RequiresRecovery = false
	}
	completion, cancel := context.WithTimeout(context.Background(), time.Second)
	saveErr := entry.binding.Repository.SaveApplication(completion, recovery.Revision, app.ID, app)
	cancel()
	app.ReceiptDurable = saveErr == nil
	if saveErr != nil {
		app.RequiresRecovery = true
		recovery.Outcome = "recovery_required"
		recovery.Error = "receipt_durability_unknown"
		err = errors.Join(err, domain.ErrDurabilityUnknown, saveErr)
	}
	s.publish(entry, app)
	return cloneApplication(app), err
}
