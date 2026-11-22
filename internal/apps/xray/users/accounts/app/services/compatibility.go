package services

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type InboundReader interface {
	Get(context.Context, string, string) (domain.Inbound, error)
	Select(context.Context, string, []string) ([]domain.Inbound, error)
}

func check(inbound domain.Inbound, actual string, generation uint64) (*domain.Guidance, error) {
	g := &domain.Guidance{Protocol: inbound.Protocol, ExpectedAccountType: inbound.AccountType, ActualAccountType: actual, Reason: inbound.Reason, NextAction: "supply_matching_account"}
	switch {
	case inbound.Err != nil:
		return guidance(inbound.Err, domain.NotApplied), inbound.Err
	case inbound.Generation != generation:
		g.NextAction = "refresh_compatibility"
		return g, domain.ErrExecutionChanged
	case !inbound.LiveAccounts:
		g.NextAction = "replace_inbound_configuration"
		return g, domain.ErrUnsupported
	case actual != inbound.AccountType:
		g.Reason = "account_type_mismatch"
		g.NextAction = "use_expected_account_type"
		return g, domain.ErrIncompatible
	default:
		g.Reason = "compatible"
		g.NextAction = "none"
		return g, nil
	}
}
func (s *Service) CheckCompatibility(ctx context.Context, id, tag string, accountType *string) (domain.Compatibility, error) {
	if accountType != nil && validators.Selector(*accountType) != nil {
		return domain.Compatibility{}, domain.ErrInvalidArgument
	}
	_, obs, release, err := s.begin(ctx, id, tag)
	if err != nil {
		return domain.Compatibility{}, err
	}
	defer release()
	started := time.Now()
	inbound, err := s.inbounds.Get(ctx, id, tag)
	if err != nil {
		return domain.Compatibility{}, err
	}
	if err = s.finish(ctx, &obs, started, nil); err != nil {
		return domain.Compatibility{}, err
	}
	result := domain.Compatibility{Observation: obs, Inbound: inbound, RequestedAccountType: accountType, Guidance: domain.Guidance{Protocol: inbound.Protocol, ExpectedAccountType: inbound.AccountType, Reason: inbound.Reason, NextAction: "supply_matching_account"}}
	if accountType != nil {
		guidance, matchErr := check(inbound, *accountType, obs.Generation)
		compatible := matchErr == nil
		result.Compatible = &compatible
		result.Guidance = *guidance
	} else if !inbound.LiveAccounts {
		result.Guidance.NextAction = "replace_inbound_configuration"
	}
	return result, nil
}
func (s *Service) checkedAdd(ctx context.Context, c Client, id, tag string, generation uint64, content []byte) (domain.NativeResult, error) {
	prepared, err := c.Prepare(content)
	if err != nil {
		return domain.NativeResult{Effect: domain.NotApplied, RequiresInstanceRestart: errors.Is(err, domain.ErrUncertain), Guidance: guidance(err, domain.NotApplied)}, err
	}
	return s.checkedPreparedAdd(ctx, id, tag, generation, prepared.AccountType(), func() (domain.NativeResult, error) { return prepared.Add(ctx, tag) })
}

func (s *Service) checkedPreparedAdd(ctx context.Context, id, tag string, generation uint64, actual string, submit func() (domain.NativeResult, error)) (domain.NativeResult, error) {
	inbound, err := s.inbounds.Get(ctx, id, tag)
	if err != nil {
		return domain.NativeResult{Effect: domain.NotApplied, RequiresInstanceRestart: errors.Is(err, domain.ErrUncertain), Guidance: guidance(err, domain.NotApplied)}, err
	}
	g, err := check(inbound, actual, generation)
	if err != nil {
		return domain.NativeResult{Effect: domain.NotApplied, Guidance: g}, err
	}
	op := domain.Operation{InstanceID: id, Generation: generation, Effect: domain.NotApplied}
	if err = s.observeMutation(ctx, &op, ctx.Err()); err != nil {
		return domain.NativeResult{Effect: domain.NotApplied, RequiresInstanceRestart: errors.Is(err, domain.ErrUncertain), Guidance: guidance(err, domain.NotApplied)}, err
	}
	return submit()
}
func (s *Service) prepareBatch(ctx context.Context, id string, client Client, items []domain.Addition, prepared []domain.PreparedAccount, results []domain.ItemResult) error {
	tags := make([]string, len(items))
	types := make([]string, len(items))
	for i, item := range items {
		tags[i] = item.InboundTag
		if !errors.Is(results[i].Err, domain.ErrNotAttempted) {
			continue
		}
		account, err := client.Prepare(item.Configuration)
		if err != nil {
			results[i].Err = err
			results[i].Operation.Guidance = guidance(err, domain.NotApplied)
			continue
		}
		prepared[i] = account
		types[i] = account.AccountType()
	}
	return s.checkBatchTypes(ctx, id, tags, types, results)
}

func (s *Service) checkBatchTypes(ctx context.Context, id string, itemTags, types []string, results []domain.ItemResult) error {
	tags := make([]string, 0)
	seen := make(map[string]bool)
	for i, tag := range itemTags {
		if errors.Is(results[i].Err, domain.ErrNotAttempted) && !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}
	if len(tags) == 0 {
		return nil
	}
	inbounds, err := s.inbounds.Select(ctx, id, tags)
	if err != nil {
		return err
	}
	if len(inbounds) != len(tags) {
		return domain.ErrInvalidResponse
	}
	byTag := make(map[string]domain.Inbound, len(inbounds))
	for i, inbound := range inbounds {
		if inbound.Tag != tags[i] {
			return domain.ErrInvalidResponse
		}
		byTag[inbound.Tag] = inbound
	}
	for i, tag := range itemTags {
		if !errors.Is(results[i].Err, domain.ErrNotAttempted) {
			continue
		}
		g, err := check(byTag[tag], types[i], results[i].Operation.Generation)
		if err != nil {
			results[i].Err = err
			results[i].Operation.Guidance = g
		}
	}
	return ctx.Err()
}
func guidance(err error, effect domain.Effect) *domain.Guidance {
	if err == nil {
		return nil
	}
	g := &domain.Guidance{Reason: "operation_failed", NextAction: "inspect_current_state"}
	switch {
	case effect == domain.Unknown:
		g.Reason = "unknown_effect"
		g.NextAction = "inspect_current_state"
	case errors.Is(err, domain.ErrNotAttempted):
		g.Reason = "not_attempted"
		g.NextAction = "resolve_prior_item"
	case errors.Is(err, domain.ErrInboundMissing):
		g.Reason = "inbound_not_found"
		g.NextAction = "select_existing_inbound"
	case errors.Is(err, domain.ErrInboundAmbiguous):
		g.Reason = "ambiguous_inbound"
		g.NextAction = "select_unique_inbound"
	case errors.Is(err, domain.ErrUncertain):
		g.Reason = "execution_fenced"
		g.NextAction = "replace_execution_before_replay"
	case errors.Is(err, domain.ErrCapacity):
		g.Reason = "resource_exhausted"
		g.NextAction = "reduce_request_or_wait_for_capacity"
	case errors.Is(err, domain.ErrInvalidArgument):
		g.Reason = "invalid_account_input"
		g.NextAction = "correct_input"
	case errors.Is(err, domain.ErrExecutionChanged):
		g.Reason = "execution_changed"
		g.NextAction = "refresh_compatibility"
	case errors.Is(err, domain.ErrUnsupported):
		g.Reason = "unsupported"
		g.NextAction = "check_native_facilities"
	}
	return g
}
