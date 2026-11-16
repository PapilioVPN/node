package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"

	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
)

// RemoveInboundUser submits one exact AlterInbound operation, with no read/scan,
// persistence, application retry or claim that existing connections were closed.
func (c *Adapter) RemoveInboundUser(ctx context.Context, selection domain.InboundUserSelection) (domain.UserMutationAcknowledgement, error) {
	if err := ctx.Err(); err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	ctx, done, err := c.beginMutation(ctx)
	if err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	defer done()
	operation := &native.RemoveUserOperation{Email: selection.Email}
	payload, err := encodeUserOperation(operation)
	if err != nil {
		return domain.UserMutationAcknowledgement{}, errors.Join(domain.ErrInvalidUserSelection, err)
	}
	client, err := c.API.HandlerClient()
	if err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	_, err = client.AlterInbound(ctx, &native.AlterInboundRequest{Tag: selection.InboundTag, Operation: payload})
	if err != nil {
		return domain.UserMutationAcknowledgement{}, fmt.Errorf("remove Xray inbound user: %w", userMutationCause(ctx, err))
	}
	return domain.UserMutationAcknowledgement{AcknowledgedAt: time.Now().UTC()}, nil
}
