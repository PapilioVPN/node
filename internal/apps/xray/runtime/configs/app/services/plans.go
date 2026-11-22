package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/policies"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

// Runtime is the existing instance owner's lifecycle/status/readiness contract.
// It deliberately does not make a native endpoint an identity attestation.
type Runtime interface {
	Restart(context.Context, string) (instances.Control, error)
	Status(context.Context, string) (instances.Status, error)
	Check(context.Context, string) (instances.Readiness, error)
}
type SourceCompiler interface {
	Compile(context.Context, string, []domain.Source) (domain.Compilation, error)
}
type Comparison interface {
	Compare(context.Context, []domain.Source, []domain.Source, []byte, []byte) (domain.Assessment, error)
}
type ReviewedRepository interface {
	Repository
	WriteReviewed(context.Context, uint64, []domain.Source, domain.Desired, domain.Application) (domain.Persistence, error)
	ReadApplication(context.Context) (domain.StoredApplication, error)
	SaveApplication(context.Context, uint64, string, domain.Application) error
	WriteRecovery(context.Context, uint64, string, string) (domain.StoredApplication, domain.Persistence, error)
}
type BaselineReader interface {
	Read(context.Context) (domain.Desired, error)
}
type LiveApplier interface {
	ApplyRouting(context.Context, []byte) (domain.LiveResult, error)
}
type PlanBinding struct {
	ID         string
	Repository ReviewedRepository
	Baseline   BaselineReader
	Live       LiveApplier
}
type savedPlan struct {
	plan     domain.Plan
	expires  time.Time
	sources  []domain.Source
	previous domain.Desired
	payload  []byte
}
type plannedEntry struct {
	binding PlanBinding
	pending *savedPlan
	last    *domain.Application
}

// Plans shares StateService's two whole-operation slots and per-instance writer
// with legacy writes. One pending plan per instance expires after one minute;
// at most eight plans share 32 MiB of source/live payload retention. Durable
// evidence retains one application and one previous source collection per store.
type Plans struct {
	state      *StateService
	compiler   SourceCompiler
	comparison Comparison
	runtime    Runtime
	epoch      string
	mu         sync.Mutex
	entries    map[string]*plannedEntry
}

func NewPlans(state *StateService, compiler SourceCompiler, comparison Comparison, runtime Runtime, bindings []PlanBinding) (*Plans, error) {
	if state == nil || compiler == nil || comparison == nil || runtime == nil {
		return nil, domain.ErrInvalidArgument
	}
	s := &Plans{state: state, compiler: compiler, comparison: comparison, runtime: runtime, epoch: identity(), entries: make(map[string]*plannedEntry, len(bindings))}
	for _, binding := range bindings {
		if state.entries[binding.ID] == nil || s.entries[binding.ID] != nil {
			return nil, domain.ErrInvalidArgument
		}
		s.entries[binding.ID] = &plannedEntry{binding: binding}
	}
	return s, nil
}
func identity() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, b := range []byte(id) {
		if !(b >= 'a' && b <= 'f' || b >= '0' && b <= '9') {
			return false
		}
	}
	return true
}
func ownSources(sources []domain.Source) []domain.Source {
	out := make([]domain.Source, len(sources))
	for i, source := range sources {
		out[i] = domain.Source{Name: strings.Clone(source.Name), Content: bytes.Clone(source.Content)}
	}
	return out
}
func (s *Plans) selected(ctx context.Context, id string) (*plannedEntry, error) {
	if _, err := s.state.selectEntry(ctx, id); err != nil {
		return nil, err
	}
	entry := s.entries[id]
	if entry == nil || entry.binding.Repository == nil {
		return nil, domain.ErrUnsupported
	}
	return entry, nil
}
func (s *Plans) previous(ctx context.Context, entry *plannedEntry, expected uint64) (domain.Desired, error) {
	previous, err := entry.binding.Repository.Read(ctx)
	if err != nil {
		return domain.Desired{}, err
	}
	if previous.Metadata.Revision != expected {
		return domain.Desired{}, domain.ErrConflict
	}
	if expected == 0 {
		if entry.binding.Baseline == nil {
			return domain.Desired{}, domain.ErrUnsupported
		}
		return entry.binding.Baseline.Read(ctx)
	}
	if !previous.Metadata.Durable {
		return domain.Desired{}, domain.ErrDurabilityUnknown
	}
	return previous, nil
}
func (s *Plans) authoritative(ctx context.Context, entry *plannedEntry, previous domain.Desired, state instances.Status) bool {
	if previous.Metadata.Revision == 0 || state.Execution.State != instances.Running || state.Execution.ExecutionID == "" {
		return false
	}
	if p := state.Execution.ConfigurationRevision; p != nil && *p == previous.Metadata.Revision {
		return true
	}
	saved, err := entry.binding.Repository.ReadApplication(ctx)
	if err != nil {
		return false
	}
	app := saved.Application
	return app.Revision == previous.Metadata.Revision && app.ReceiptDurable && !app.RequiresRecovery && (app.Outcome == "completed" || app.Outcome == "unchanged") && app.ExecutionID == state.Execution.ExecutionID && app.Generation == state.Execution.Generation
}

func (s *Plans) Plan(ctx context.Context, id string, expected uint64, sources []domain.Source) (domain.Plan, error) {
	if expected == math.MaxUint64 {
		return domain.Plan{}, domain.ErrCapacity
	}
	if err := validators.Sources(sources); err != nil {
		return domain.Plan{}, err
	}
	ctx, _, release, err := s.state.begin(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	defer release()
	entry, err := s.selected(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	previous, err := s.previous(ctx, entry, expected)
	if err != nil {
		return domain.Plan{}, err
	}
	before, err := s.compiler.Compile(ctx, id, previous.Sources)
	if err != nil {
		return domain.Plan{}, err
	}
	after := before
	if !policies.SameSources(previous.Sources, sources) {
		after, err = s.compiler.Compile(ctx, id, sources)
		if err != nil {
			return domain.Plan{}, err
		}
	}
	return s.review(ctx, id, entry, previous, sources, before.Content, after.Content, false)
}
func (s *Plans) review(ctx context.Context, id string, entry *plannedEntry, previous domain.Desired, sources []domain.Source, before, after []byte, forceRestart bool) (domain.Plan, error) {
	assessment, err := s.comparison.Compare(ctx, previous.Sources, sources, before, after)
	if err != nil {
		return domain.Plan{}, err
	}
	state, err := s.runtime.Status(ctx, id)
	if err != nil {
		return domain.Plan{}, executionError(err)
	}
	if forceRestart {
		assessment.Mode = domain.Restart
		assessment.Reason = "selected_membership_requires_authoritative_replacement"
	} else if !s.authoritative(ctx, entry, previous, state) {
		assessment.Mode = domain.Restart
		assessment.Reason = "current_execution_not_bound_to_desired_revision"
	} else if assessment.Mode == domain.LiveRouting && entry.binding.Live == nil {
		assessment.Mode = domain.Restart
		assessment.Reason = "native_routing_api_not_configured"
	}
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	started := time.Now()
	plan := domain.Plan{ID: identity(), InstanceID: strings.Clone(id), ExpectedRevision: previous.Metadata.Revision, PreviousSHA256: policies.SourceDigest(previous.Sources), CandidateSHA256: policies.SourceDigest(sources), ExecutionID: state.Execution.ExecutionID, Generation: state.Execution.Generation, Mode: assessment.Mode, Reason: assessment.Reason, Differences: append([]domain.Difference{}, assessment.Differences...), ConnectionsRetained: assessment.Mode != domain.Restart, CreatedAt: started.UTC(), ExpiresAt: started.Add(time.Minute).UTC()}
	saved := &savedPlan{plan: plan, expires: started.Add(time.Minute), sources: ownSources(sources), previous: previous}
	if plan.Mode == domain.LiveRouting {
		saved.payload = assessment.Payload
	}
	size := len(saved.payload)
	for _, source := range saved.sources {
		size += len(source.Name) + len(source.Content)
	}
	for _, source := range previous.Sources {
		size += len(source.Name) + len(source.Content)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	used, count := 0, 0
	for key, e := range s.entries {
		if e.pending != nil && time.Now().After(e.pending.expires) {
			e.pending = nil
		}
		if key == id || e.pending == nil {
			continue
		}
		count++
		used += len(e.pending.payload)
		for _, source := range e.pending.sources {
			used += len(source.Name) + len(source.Content)
		}
		for _, source := range e.pending.previous.Sources {
			used += len(source.Name) + len(source.Content)
		}
	}
	if count >= 8 || used+size > 32<<20 {
		return domain.Plan{}, domain.ErrCapacity
	}
	entry.pending = saved
	return plan, nil
}
func cloneApplication(app domain.Application) domain.Application {
	if app.CompletedAt != nil {
		v := *app.CompletedAt
		app.CompletedAt = &v
	}
	if app.Recovery != nil {
		v := *app.Recovery
		if v.CompletedAt != nil {
			at := *v.CompletedAt
			v.CompletedAt = &at
		}
		app.Recovery = &v
	}
	return app
}
func (s *Plans) publish(entry *plannedEntry, app domain.Application) {
	s.mu.Lock()
	copy := cloneApplication(app)
	entry.last = &copy
	s.mu.Unlock()
}

// Application is independently readable during native waits. After Node death,
// a persisted in-progress record is UNKNOWN, never proof of an unsent operation.
func (s *Plans) Application(ctx context.Context, id, applicationID string) (domain.Application, error) {
	if !validID(applicationID) {
		return domain.Application{}, domain.ErrInvalidArgument
	}
	entry, err := s.selected(ctx, id)
	if err != nil {
		return domain.Application{}, err
	}
	s.mu.Lock()
	if entry.last != nil && entry.last.ID == applicationID {
		app := cloneApplication(*entry.last)
		s.mu.Unlock()
		return app, nil
	}
	s.mu.Unlock()
	saved, err := entry.binding.Repository.ReadApplication(ctx)
	if err != nil {
		return domain.Application{}, err
	}
	app := saved.Application
	if app.ID != applicationID {
		return domain.Application{}, domain.ErrNoApplication
	}
	if app.Outcome == "in_progress" {
		app.Outcome = "recovery_required"
		app.Phase = "interrupted"
		app.EngineEffect = "unknown"
		app.Error = "operation_interrupted"
		app.RequiresRecovery = true
	}
	if app.Recovery != nil && app.Recovery.Outcome == "in_progress" {
		app.Recovery.Outcome = "recovery_required"
		app.Recovery.EngineEffect = "unknown"
		app.Recovery.Error = "operation_interrupted"
		app.RequiresRecovery = true
	}
	return app, nil
}
func applicationError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrStalePlan), errors.Is(err, instances.ErrExecutionChanged):
		return "stale_plan"
	case errors.Is(err, domain.ErrExecutionFailed):
		return "execution_failed"
	case errors.Is(err, domain.ErrReadiness):
		return "readiness_failed"
	case errors.Is(err, domain.ErrDurabilityUnknown):
		return "durability_unknown"
	case errors.Is(err, domain.ErrUnsupported):
		return "failed_precondition"
	case errors.Is(err, domain.ErrCapacity):
		return "resource_exhausted"
	default:
		return "unavailable"
	}
}
