package services

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type CandidateInspector interface {
	Inspect(context.Context, []byte) (domain.Candidate, error)
}
type replacementPlan struct {
	plan    domain.ReplacementPlan
	expires time.Time
	content []byte
	result  *domain.ReplacementResult
	cause   error
}

// Replacements keeps one reviewed candidate/result per instance. At most 16 MiB
// of pending candidate bytes are retained; candidates expire after one minute.
// Results contain no configuration and remain the last local result only.
type Replacements struct {
	service   *Service
	inspector CandidateInspector
	preparer  SourcePreparer
	mu        sync.Mutex
	plans     map[string]*replacementPlan
}

func NewReplacements(service *Service, inspector CandidateInspector, preparer SourcePreparer) (*Replacements, error) {
	if service == nil || inspector == nil {
		return nil, domain.ErrInvalidArgument
	}
	return &Replacements{service: service, inspector: inspector, preparer: preparer, plans: make(map[string]*replacementPlan)}, nil
}
func (r *Replacements) fingerprint(ctx context.Context, records []domain.Record, tag string) (string, error) {
	var selected *domain.Record
	for i := range records {
		if records[i].Tag == tag {
			if selected != nil {
				return "", domain.ErrInvalidArgument
			}
			selected = &records[i]
		}
	}
	if selected == nil {
		return "", domain.ErrOutboundNotFound
	}
	candidate, err := r.inspector.Inspect(ctx, selected.Configuration)
	return candidate.Fingerprint, err
}

// Plan reads one fresh native inventory, not a paged/stale/desired projection.
// A native list cannot identify the default; preserving it requires Configs.
func (r *Replacements) Plan(ctx context.Context, id string, request domain.ReplacementRequest) (domain.ReplacementPlan, error) {
	if err := validators.Replacement(request); err != nil {
		return domain.ReplacementPlan{}, err
	}
	selected, generation, release, err := r.service.beginRead(ctx, id)
	if err != nil {
		return domain.ReplacementPlan{}, err
	}
	defer release()
	candidate, err := r.inspector.Inspect(ctx, request.Configuration)
	if err != nil {
		return domain.ReplacementPlan{}, err
	}
	if candidate.Tag != request.Tag {
		return domain.ReplacementPlan{}, domain.ErrInvalidArgument
	}
	records, err := selected.client.Records(ctx)
	if err = r.service.finishRead(ctx, id, generation, err); err != nil {
		return domain.ReplacementPlan{}, err
	}
	current, err := r.fingerprint(ctx, records, request.Tag)
	if err != nil {
		return domain.ReplacementPlan{}, err
	}
	selected.mu.Lock()
	revision := selected.revision
	selected.mu.Unlock()
	started := time.Now()
	now := started.UTC()
	plan := domain.ReplacementPlan{Validation: "handler_not_constructed", TrafficGapPossible: true, NextAction: "apply_plan", ID: token(), InstanceID: strings.Clone(id), Generation: generation, OwnerRevision: revision, Tag: strings.Clone(request.Tag), Mode: "live_remove_add", Reason: "explicit_default_change_and_retained_connections", DefaultState: "unobservable", CurrentSHA256: current, CandidateSHA256: candidate.Fingerprint, DefaultMayChange: true, ExistingConnectionsRetained: true, NativeResourcesMayRemain: true, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if request.ConnectionPolicy == domain.TerminateConnections {
		plan.Mode = "configs_restart"
		plan.Reason = "native_remove_does_not_close_connections_or_resources"
	} else if current == candidate.Fingerprint {
		plan.Mode = "unchanged"
		plan.Reason = "equal_native_projection"
		plan.DefaultMayChange = false
		plan.ExistingConnectionsRetained = true
		plan.NativeResourcesMayRemain = false
		plan.TrafficGapPossible = false
	} else if request.DefaultPolicy == domain.PreserveDefault {
		plan.Mode = "configs_restart"
		plan.Reason = "default_is_not_observable_or_assignable_by_native_api"
	}
	if plan.Mode == "configs_restart" {
		plan.NextAction = "replace_ordered_sources_with_configs"
		plan.ExistingConnectionsRetained = false
		plan.NativeResourcesMayRemain = false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	used := 0
	for key, old := range r.plans {
		if old.result == nil && time.Now().After(old.expires) {
			old.content = nil
		}
		if key != id {
			used += len(old.content)
		}
	}
	if old := r.plans[id]; old != nil && old.result != nil && old.result.Outcome == "in_progress" {
		return domain.ReplacementPlan{}, domain.ErrCapacity
	}
	if used+len(request.Configuration) > 16<<20 {
		return domain.ReplacementPlan{}, domain.ErrCapacity
	}
	r.plans[strings.Clone(id)] = &replacementPlan{plan: plan, expires: started.Add(time.Minute), content: bytes.Clone(request.Configuration)}
	return plan, nil
}
func cloneReplacement(result domain.ReplacementResult) domain.ReplacementResult {
	result.Steps = append([]domain.ReplacementStep{}, result.Steps...)
	for i := range result.Steps {
		if p := result.Steps[i].CompletedAt; p != nil {
			v := *p
			result.Steps[i].CompletedAt = &v
		}
	}
	if result.CompletedAt != nil {
		v := *result.CompletedAt
		result.CompletedAt = &v
	}
	return result
}
func (r *Replacements) Result(ctx context.Context, id, planID string) (domain.ReplacementResult, error) {
	if err := validators.PlanID(planID); err != nil {
		return domain.ReplacementResult{}, err
	}
	if _, err := r.service.selectEntry(ctx, id); err != nil {
		return domain.ReplacementResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	saved := r.plans[id]
	if saved == nil || saved.plan.ID != planID {
		return domain.ReplacementResult{}, domain.ErrPlanExpired
	}
	if saved.result == nil {
		if time.Now().After(saved.expires) {
			saved.content = nil
			return domain.ReplacementResult{}, domain.ErrPlanExpired
		}
		return domain.ReplacementResult{PlanID: planID, InstanceID: id, Tag: saved.plan.Tag, Generation: saved.plan.Generation, Outcome: "planned", Steps: []domain.ReplacementStep{}}, nil
	}
	return cloneReplacement(*saved.result), nil
}

// Apply consumes a plan once. Identical replay returns its bounded evidence,
// including while in flight; it never reruns Remove/Add after a missing reply.
func (r *Replacements) Apply(ctx context.Context, id, planID string) (domain.ReplacementResult, error) {
	if err := validators.PlanID(planID); err != nil {
		return domain.ReplacementResult{}, err
	}
	if _, err := r.service.selectEntry(ctx, id); err != nil {
		return domain.ReplacementResult{}, err
	}
	r.mu.Lock()
	saved := r.plans[id]
	if saved == nil || saved.plan.ID != planID {
		r.mu.Unlock()
		return domain.ReplacementResult{}, domain.ErrPlanExpired
	}
	if saved.result != nil {
		result, cause := cloneReplacement(*saved.result), saved.cause
		r.mu.Unlock()
		return result, cause
	}
	if time.Now().After(saved.expires) {
		saved.content = nil
		r.mu.Unlock()
		return domain.ReplacementResult{}, domain.ErrPlanExpired
	}
	plan := saved.plan
	content := saved.content
	saved.result = &domain.ReplacementResult{PlanID: plan.ID, InstanceID: plan.InstanceID, Tag: plan.Tag, Generation: plan.Generation, Outcome: "in_progress", StartedAt: time.Now().UTC(), Steps: []domain.ReplacementStep{{Kind: "remove", Effect: "not_attempted"}, {Kind: "add", Effect: "not_attempted"}}}
	r.mu.Unlock()
	publish := func(index int, step domain.ReplacementStep) {
		r.mu.Lock()
		saved.result.Steps[index] = step
		if step.Attempted && step.Effect != "not_applied" {
			saved.result.DefaultMayChange = true
		}
		if index == 0 {
			saved.result.NativeResourcesMayRemain = step.Effect == "completed" || step.Effect == "unknown" || step.Effect == "in_progress"
		}
		r.mu.Unlock()
	}
	var operation domain.Operation
	var operationErr error
	if plan.Mode == "configs_restart" {
		operationErr = domain.ErrRestartRequired
	} else {
		operation, operationErr = r.service.mutate(ctx, id, "replace", plan.Tag, func(client Client, generation uint64) (domain.NativeResult, error) {
			selected := r.service.entries[id]
			selected.mu.Lock()
			revision := selected.revision
			selected.mu.Unlock()
			if generation != plan.Generation || revision != plan.OwnerRevision {
				return domain.NativeResult{Effect: domain.NotApplied}, domain.ErrStalePlan
			}
			records, err := client.Records(ctx)
			if err != nil {
				return domain.NativeResult{Effect: domain.NotApplied}, err
			}
			current, err := r.fingerprint(ctx, records, plan.Tag)
			if err != nil {
				return domain.NativeResult{Effect: domain.NotApplied}, err
			}
			if current != plan.CurrentSHA256 {
				return domain.NativeResult{Effect: domain.NotApplied}, domain.ErrStalePlan
			}
			if err := r.service.finishRead(ctx, id, generation, nil); err != nil {
				return domain.NativeResult{Effect: domain.NotApplied}, err
			}
			if plan.Mode == "unchanged" {
				return domain.NativeResult{Effect: domain.NotApplied}, nil
			}
			completed := false
			for i, kind := range []string{"remove", "add"} {
				state, err := r.service.status.Status(ctx, id)
				if err == nil && (state.Execution.Generation != generation || state.Execution.State != instances.Running) {
					err = domain.ErrExecutionChanged
				}
				if err == nil {
					err = ctx.Err()
				}
				if err != nil {
					effect := domain.NotApplied
					if completed {
						effect = domain.Effect("partial")
					}
					return domain.NativeResult{Effect: effect}, err
				}
				step := domain.ReplacementStep{Kind: kind, Attempted: true, Effect: "in_progress"}
				publish(i, step)
				var native domain.NativeResult
				if i == 0 {
					native, err = client.Remove(ctx, plan.Tag)
				} else {
					native, err = client.Add(ctx, content)
				}
				if err == nil && (!native.Acknowledged || native.Effect != domain.Completed) {
					native.Effect = domain.Unknown
					native.RequiresInstanceRestart = true
					err = errors.New("unconfirmed outbound mutation")
				}
				step.Effect = string(native.Effect)
				step.Acknowledged = native.Acknowledged
				step.RequiresInstanceRestart = native.RequiresInstanceRestart
				step.Error = replacementError(err)
				at := time.Now().UTC()
				step.CompletedAt = &at
				after, statusErr := r.service.status.Status(context.WithoutCancel(ctx), id)
				if statusErr == nil && (after.Execution.Generation != generation || after.Execution.State != instances.Running) {
					statusErr = domain.ErrExecutionChanged
				}
				if statusErr != nil && native.Effect != domain.NotApplied {
					step.Effect = "unknown"
					step.RequiresInstanceRestart = true
					err = errors.Join(err, statusErr)
					step.Error = replacementError(err)
				}
				publish(i, step)
				if err != nil {
					effect := native.Effect
					if completed && effect == domain.NotApplied {
						effect = domain.Effect("partial")
					}
					return domain.NativeResult{Effect: effect, RequiresInstanceRestart: step.RequiresInstanceRestart}, err
				}
				completed = true
			}
			return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
		})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result := saved.result
	now := time.Now().UTC()
	result.CompletedAt = &now
	result.Error = replacementError(operationErr)
	ack, unknown := false, false
	for _, step := range result.Steps {
		ack = ack || step.Acknowledged
		unknown = unknown || step.Effect == "unknown" || step.Effect == "in_progress"
		result.RequiresInstanceRestart = result.RequiresInstanceRestart || step.RequiresInstanceRestart
	}
	result.NativeResourcesMayRemain = result.Steps[0].Effect == "completed" || result.Steps[0].Effect == "unknown" || result.Steps[0].Effect == "in_progress"
	result.DefaultMayChange = false
	for _, step := range result.Steps {
		result.DefaultMayChange = result.DefaultMayChange || (step.Attempted && step.Effect != "not_applied")
	}
	result.RequiresInstanceRestart = result.RequiresInstanceRestart || operation.RequiresInstanceRestart || plan.Mode == "configs_restart"
	switch {
	case unknown || operation.Effect == domain.Unknown:
		result.Outcome = "unknown"
	case operationErr != nil && ack:
		result.Outcome = "partial"
	case operationErr != nil:
		result.Outcome = "not_applied"
	case plan.Mode == "unchanged":
		result.Outcome = "unchanged"
	default:
		result.Outcome = "completed"
	}
	saved.content = nil
	saved.cause = operationErr
	return cloneReplacement(*result), operationErr
}
func replacementError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, domain.ErrStalePlan), errors.Is(err, domain.ErrExecutionChanged):
		return "stale_plan"
	case errors.Is(err, domain.ErrRestartRequired):
		return "configs_restart_required"
	case errors.Is(err, domain.ErrOutboundNotFound):
		return "outbound_not_found"
	case errors.Is(err, domain.ErrCapacity):
		return "resource_exhausted"
	case errors.Is(err, domain.ErrUncertain):
		return "restart_required"
	case errors.Is(err, domain.ErrUnsupported):
		return "failed_precondition"
	default:
		return "unavailable"
	}
}

// PlanSources compiles the ordered source set once, selecting only the requested
// outbound for the live plan. Restart fallback still requires reviewing/publishing
// complete original sources through Configs; it never treats this selection as a
// lossless replacement for desired configuration or source-level environment.
func (r *Replacements) PlanSources(ctx context.Context, id, tag string, sources []configs.Source, defaultPolicy domain.DefaultPolicy, connections domain.ConnectionPolicy) (domain.ReplacementPlan, error) {
	if err := validators.Tag(tag); err != nil {
		return domain.ReplacementPlan{}, err
	}
	if err := validators.Policies(defaultPolicy, connections); err != nil {
		return domain.ReplacementPlan{}, err
	}
	if r.preparer == nil {
		return domain.ReplacementPlan{}, domain.ErrUnsupported
	}
	content, err := r.preparer.Prepare(ctx, id, tag, sources)
	if err != nil {
		return domain.ReplacementPlan{}, err
	}
	return r.Plan(ctx, id, domain.ReplacementRequest{Tag: tag, Configuration: content, DefaultPolicy: defaultPolicy, ConnectionPolicy: connections})
}
