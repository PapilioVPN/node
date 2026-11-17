package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

// AddInboundUsers encodes the complete validated collection before any native
// effects, inspects its inbound once, then submits ordered individual additions.
// Xray has no native bulk operation: this is a non-atomic 1+N fallback. All calls
// share one admission slot and deadline, stopping at the first failure without
// retries, compensation, persisted config changes or goroutines.
func (c *Adapter) AddInboundUsers(ctx context.Context, input domain.InboundUserAdditions) (domain.UserAdditionsReport, error) {
	if err := ctx.Err(); err != nil {
		return domain.UserAdditionsReport{}, err
	}
	if len(input.Users) == 0 {
		// An empty addition has no effects and makes no engine-availability claim.
		return domain.UserAdditionsReport{Items: []domain.UserAdditionResult{}}, nil
	}
	ctx, done, err := c.beginMutation(ctx)
	if err != nil {
		return domain.UserAdditionsReport{}, err
	}
	defer done()
	report := domain.UserAdditionsReport{Items: make([]domain.UserAdditionResult, len(input.Users))}
	for i, user := range input.Users {
		report.Items[i].Email = user.Email
	}
	operations := make([]*serial.TypedMessage, len(input.Users))
	var expectedType string
	for i, item := range input.Users {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		user, inboundType, err := nativeUser(item)
		if err != nil {
			return report, err
		}
		if i == 0 {
			expectedType = inboundType
		} else if inboundType != expectedType {
			return report, domain.ErrInboundProtocolMismatch
		}
		operation, err := encodeUserOperation(&native.AddUserOperation{User: user})
		if err != nil {
			return report, errors.Join(domain.ErrInvalidUserAccount, err)
		}
		operations[i] = operation
	}
	client, err := c.API.HandlerClient()
	if err != nil {
		return report, err
	}
	if _, err := userInboundType(ctx, client, input.InboundTag, expectedType); err != nil {
		return report, err
	}
	for i, operation := range operations {
		item := &report.Items[i]
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
			return report, fmt.Errorf("add Xray inbound users: %w", cause)
		}
		item.State, item.AcknowledgedAt = domain.UserMutationAcknowledged, time.Now().UTC()
	}
	return report, nil
}
