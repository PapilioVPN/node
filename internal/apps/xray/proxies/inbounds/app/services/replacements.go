package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type replacementCandidate struct {
	plan    domain.ReplacementPlan
	content []byte
	epoch   uint64
}

func (c *Catalog) Compatibility(ctx context.Context, id, tag string) (domain.Compatibility, error) {
	if err := validators.Tag(tag); err != nil {
		return domain.Compatibility{}, err
	}

	selected, generation, release, err := c.service.beginRead(ctx, id)
	if err != nil {
		return domain.Compatibility{}, err
	}
	defer release()
	if selected.projection == nil {
		return domain.Compatibility{}, domain.ErrUnsupported
	}
	started := time.Now()
	summary, err := selected.projection.Compatibility(ctx, tag)
	if err = c.service.finishRead(ctx, id, generation, err); err != nil {
		return domain.Compatibility{}, err
	}
	return domain.Compatibility{InstanceID: id, Generation: generation, Inbound: summary, ObservedAt: time.Now().UTC(), Duration: time.Since(started)}, nil
}
func exactRecord(records []domain.Record, tag string) (domain.Record, error) {
	var selected domain.Record
	found := false
	for _, r := range records {
		if r.Tag == tag {
			if found {
				return domain.Record{}, domain.ErrInvalidArgument
			}
			selected = r
			found = true
		}
	}
	if !found {
		return domain.Record{}, domain.ErrStale
	}
	return selected, nil
}
func (c *Catalog) Plan(ctx context.Context, id string, input domain.ReplacementInput) (domain.ReplacementPlan, error) {
	if err := validators.Tag(input.Tag); err != nil {
		return domain.ReplacementPlan{}, err
	}
	if err := validators.Configuration(input.Configuration); err != nil {
		return domain.ReplacementPlan{}, err
	}
	candidate, err := c.inspector.Describe(input.Configuration)
	if err != nil || candidate.Tag != input.Tag {
		return domain.ReplacementPlan{}, domain.ErrInvalidArgument
	}
	candidateHash, err := c.inspector.Fingerprint(input.Configuration)
	if err != nil {
		return domain.ReplacementPlan{}, err
	}
	selected, generation, release, err := c.service.beginRead(ctx, id)
	if err != nil {
		return domain.ReplacementPlan{}, err
	}
	defer release()
	selected.mu.Lock()
	epoch := selected.epoch
	selected.mu.Unlock()
	records, err := selected.client.Records(ctx)
	if err = c.service.finishRead(ctx, id, generation, err); err != nil {
		return domain.ReplacementPlan{}, err
	}
	observed := domain.Records{Generation: generation, Inbounds: records}
	current, err := exactRecord(observed.Inbounds, input.Tag)
	if err != nil {
		return domain.ReplacementPlan{}, err
	}
	currentSummary, err := c.inspector.Describe(current.Configuration)
	if err != nil {
		return domain.ReplacementPlan{}, err
	}
	currentHash, err := c.inspector.Fingerprint(current.Configuration)
	if err != nil {
		return domain.ReplacementPlan{}, err
	}
	plan := domain.ReplacementPlan{InstanceID: strings.Clone(id), Tag: strings.Clone(input.Tag), Generation: observed.Generation, CurrentSHA256: currentHash, CandidateSHA256: candidateHash, Mode: "restart", Reason: "live_interruption_not_accepted", ExistingConnections: "terminated_by_restart", NewConnections: "interrupted_until_readiness", Fallback: "replace_configuration", ExpiresAt: time.Now().UTC().Add(pageLifetime)}
	if input.AllowInterruption && !input.DisconnectExisting {
		plan.Mode, plan.Reason, plan.ExistingConnections, plan.NewConnections = "live", "remove_then_add_non_atomic", "may_survive_with_old_settings", "listener_gap"
	}
	if input.DisconnectExisting {
		plan.Reason = "disconnect_existing_connections"
	}
	if candidate.Protocol == "unknown" || candidate.Reason == "client_mode" || currentSummary.Protocol == "unknown" || currentSummary.Reason == "client_mode" {
		plan.Mode, plan.Reason, plan.ExistingConnections, plan.NewConnections = "restart", "unsupported_live_structure", "terminated_by_restart", "interrupted_until_readiness"
	}
	// No native membership reconstruction or automatic rollback is possible: the
	// readback contains original settings, not the current live account collection.
	if err := ctx.Err(); err != nil {
		return domain.ReplacementPlan{}, err
	}
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return domain.ReplacementPlan{}, err
	}
	plan.Token = hex.EncodeToString(key)
	c.plansMu.Lock()
	defer c.plansMu.Unlock()
	if c.plans == nil {
		c.plans = make(map[string]replacementCandidate)
	}
	for token, old := range c.plans {
		if !time.Now().Before(old.plan.ExpiresAt) {
			delete(c.plans, token)
		}
	}
	if len(c.plans) >= 8 {
		return domain.ReplacementPlan{}, domain.ErrCapacity
	}
	c.plans[plan.Token] = replacementCandidate{plan: plan, content: bytes.Clone(input.Configuration), epoch: epoch}
	return plan, nil
}

// Replace executes only a reviewed live plan. Restart plans deliberately name
// the existing Configs ReplaceConfiguration/ApplyConfiguration contract: the
// controller supplies complete original ordered sources and expected_revision.
// Inbounds cannot safely synthesize those from native ListInbounds readback.
func (c *Catalog) Replace(ctx context.Context, id, token string) (domain.Operation, error) {
	if len(token) != 32 {
		return domain.Operation{InstanceID: id, Kind: "replace", Effect: domain.NotApplied}, domain.ErrInvalidArgument
	}
	return c.service.mutate(ctx, id, "replace", "", func(client Client, generation uint64) (domain.NativeResult, error) {
		c.plansMu.Lock()
		candidate, ok := c.plans[token]
		if !ok || candidate.plan.InstanceID != id || !time.Now().Before(candidate.plan.ExpiresAt) || candidate.plan.Generation != generation {
			c.plansMu.Unlock()
			return domain.NativeResult{Effect: domain.NotApplied}, domain.ErrStale
		}
		if candidate.plan.Mode != "live" {
			c.plansMu.Unlock()
			return domain.NativeResult{Effect: domain.NotApplied}, domain.ErrUnsupported
		}
		delete(c.plans, token)
		c.plansMu.Unlock()
		selected := c.service.entries[id]
		selected.mu.Lock()
		epoch := selected.epoch
		selected.mu.Unlock()
		if epoch != candidate.epoch+1 {
			return domain.NativeResult{Effect: domain.NotApplied}, domain.ErrStale
		}
		progress := &domain.ReplacementProgress{Token: candidate.plan.Token, Remove: domain.ReplacementStep{Effect: domain.NotApplied}, Add: domain.ReplacementStep{Effect: domain.NotApplied}, NextAction: "review_new_plan"}
		result := domain.NativeResult{Tag: candidate.plan.Tag, Effect: domain.NotApplied, Replacement: progress}
		records, err := client.Records(ctx)
		if err != nil {
			return result, err
		}
		current, err := exactRecord(records, candidate.plan.Tag)
		if err != nil {
			return result, err
		}
		hash, err := c.inspector.Fingerprint(current.Configuration)
		if err != nil {
			return result, err
		}
		if hash != candidate.plan.CurrentSHA256 {
			return result, domain.ErrStale
		}
		state, err := c.service.status.Status(ctx, id)
		if err != nil {
			return result, err
		}
		if state.Execution.Generation != generation || state.Execution.State != instances.Running {
			return result, domain.ErrExecutionChanged
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		progress.Remove.Attempted = true
		removal, err := client.Remove(ctx, candidate.plan.Tag)
		progress.Remove.Effect, progress.Remove.Acknowledged = removal.Effect, removal.Acknowledged
		if err != nil {
			result.Effect = removal.Effect
			result.RequiresInstanceRestart = removal.RequiresInstanceRestart || removal.Effect == domain.Unknown
			if result.RequiresInstanceRestart {
				progress.NextAction = "replace_execution_then_apply_configuration"
			}
			return result, err
		}
		// An acknowledged removal is an irreversible partial effect. Do not replay
		// the old constructor config, which may silently erase live account changes.
		result.Effect, result.RequiresInstanceRestart, progress.NextAction = domain.Partial, true, "replace_configuration"
		if err := ctx.Err(); err != nil {
			return result, err
		}
		state, err = c.service.status.Status(ctx, id)
		if err != nil {
			return result, err
		}
		if state.Execution.Generation != generation || state.Execution.State != instances.Running {
			return result, domain.ErrExecutionChanged
		}
		progress.Add.Attempted = true
		addition, err := client.Add(ctx, candidate.content)
		progress.Add.Effect, progress.Add.Acknowledged = addition.Effect, addition.Acknowledged
		if err != nil {
			if addition.Effect == domain.Unknown {
				result.Effect = domain.Unknown
			}
			return result, err
		}
		result.Effect, result.Acknowledged, result.RequiresInstanceRestart, progress.NextAction = domain.Completed, true, false, "none"
		return result, nil
	})
}
