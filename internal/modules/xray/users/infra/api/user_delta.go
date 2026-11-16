package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"

	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
)

// ApplyInboundUserDelta replaces selected memberships without replaying lossy
// native account observations. Prepare every account before effects, then use one
// type lookup and one complete membership read. Xray has no native bulk: ordered
// effects share one admission/deadline and stop at the first failure. Neither
// observations nor changes are fenced against external owners or process restart.
func (c *Adapter) ApplyInboundUserDelta(ctx context.Context, input domain.InboundUserDelta) (domain.UserDeltaReport, error) {
	if err := ctx.Err(); err != nil {
		return domain.UserDeltaReport{}, err
	}
	if len(input.Users) == 0 && len(input.RemoveEmails) == 0 {
		return domain.UserDeltaReport{Steps: []domain.UserMutationStep{}}, nil
	}
	ctx, done, err := c.beginMutation(ctx)
	if err != nil {
		return domain.UserDeltaReport{}, err
	}
	defer done()
	additions, expectedType, err := prepareUserDelta(ctx, input)
	if err != nil {
		return domain.UserDeltaReport{}, err
	}
	client, err := c.API.HandlerClient()
	if err != nil {
		return domain.UserDeltaReport{}, err
	}
	kind, err := userInboundType(ctx, client, input.InboundTag, expectedType)
	if err != nil {
		return domain.UserDeltaReport{}, err
	}
	identity, duplicates, err := deltaIdentityKey(kind)
	if err != nil {
		return domain.UserDeltaReport{}, err
	}
	selected := make(map[string]struct{}, len(input.Users)+len(input.RemoveEmails))
	selectEmail := func(email string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := identity(email)
		if _, exists := selected[key]; exists {
			return domain.ErrInvalidUserDelta
		}
		// Legacy Shadowsocks lowercases removal queries before EqualFold but stores
		// original bytes. Some Unicode names cannot select their own stored identity.
		if kind == "xray.proxy.shadowsocks.ServerConfig" && key != identity(strings.ToLower(email)) {
			return domain.ErrInvalidUserDelta
		}
		selected[key] = struct{}{}
		return nil
	}
	for _, email := range input.RemoveEmails {
		if err := selectEmail(email); err != nil {
			return domain.UserDeltaReport{}, err
		}
	}
	for _, user := range input.Users {
		if err := selectEmail(user.Email); err != nil {
			return domain.UserDeltaReport{}, err
		}
	}
	response, err := client.GetInboundUsers(ctx, &native.GetInboundUserRequest{Tag: input.InboundTag})
	if ctx.Err() != nil {
		return domain.UserDeltaReport{}, errors.Join(ctx.Err(), err)
	}
	if err != nil {
		return domain.UserDeltaReport{}, userObservationError(err)
	}
	if response == nil {
		return domain.UserDeltaReport{}, domain.ErrInvalidUserObservation
	}
	// Only membership is needed. Do not decode/copy unrelated account payloads.
	counts := make(map[string]int, len(selected))
	for _, member := range response.Users {
		if err := ctx.Err(); err != nil {
			return domain.UserDeltaReport{}, err
		}
		if member == nil {
			return domain.UserDeltaReport{}, domain.ErrInvalidUserObservation
		}
		key := identity(member.Email)
		if _, exists := selected[key]; !exists {
			continue
		}
		counts[key]++
		if !duplicates && counts[key] > 1 {
			return domain.UserDeltaReport{}, domain.ErrInvalidUserObservation
		}
	}
	report, operations, err := planUserDelta(ctx, input, additions, counts, identity)
	if err != nil {
		return domain.UserDeltaReport{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.UserDeltaReport{}, err
	}
	report.ObservedAt = time.Now().UTC()
	for i, operation := range operations {
		item := &report.Steps[i]
		if item.State == domain.UserMutationNotRequired {
			continue
		}
		if err := ctx.Err(); err != nil {
			item.State, item.Failure = domain.UserMutationNotApplied, err
			return report, err
		}
		_, err := client.AlterInbound(ctx, &native.AlterInboundRequest{Tag: input.InboundTag, Operation: operation})
		if err != nil {
			cause := userMutationCause(ctx, err)
			item.State, item.Failure = domain.UserMutationNotApplied, cause
			if errors.Is(cause, domain.ErrUserMutationUnknown) {
				item.State = domain.UserMutationUnknown
			}
			return report, fmt.Errorf("apply Xray user delta: %w", cause)
		}
		item.State, item.AcknowledgedAt = domain.UserMutationAcknowledged, time.Now().UTC()
	}
	return report, nil
}

// planUserDelta emits requested removals first, then each selected replacement's
// old memberships followed by its addition. Parallel slices retain exact progress
// without exposing native payloads in the report. No effects occur while planning.
func planUserDelta(ctx context.Context, input domain.InboundUserDelta, additions []*serial.TypedMessage, counts map[string]int, identity func(string) string) (domain.UserDeltaReport, []*serial.TypedMessage, error) {
	report := domain.UserDeltaReport{Steps: []domain.UserMutationStep{}}
	var operations []*serial.TypedMessage
	remove := func(email string) error {
		count := counts[identity(email)]
		if count == 0 {
			return nil
		}
		operation, err := encodeUserOperation(&native.RemoveUserOperation{Email: email})
		if err != nil {
			return errors.Join(domain.ErrInvalidUserSelection, err)
		}
		for range count {
			if err := ctx.Err(); err != nil {
				return err
			}
			report.Steps = append(report.Steps, domain.UserMutationStep{Email: email, Action: domain.UserMutationRemove})
			operations = append(operations, operation)
		}
		return nil
	}
	for _, email := range input.RemoveEmails {
		if err := ctx.Err(); err != nil {
			return domain.UserDeltaReport{}, nil, err
		}
		if counts[identity(email)] == 0 {
			report.Steps = append(report.Steps, domain.UserMutationStep{Email: email, Action: domain.UserMutationRemove, State: domain.UserMutationNotRequired})
			operations = append(operations, nil)
		} else if err := remove(email); err != nil {
			return domain.UserDeltaReport{}, nil, err
		}
	}
	for i, user := range input.Users {
		if err := ctx.Err(); err != nil {
			return domain.UserDeltaReport{}, nil, err
		}
		if err := remove(user.Email); err != nil {
			return domain.UserDeltaReport{}, nil, err
		}
		report.Steps = append(report.Steps, domain.UserMutationStep{Email: user.Email, Action: domain.UserMutationAdd})
		operations = append(operations, additions[i])
	}
	return report, operations, nil
}
