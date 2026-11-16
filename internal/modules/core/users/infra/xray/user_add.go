package xray

import (
	"context"
	"errors"
	"fmt"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/core/users/domain"
)

// AddInboundUser checks the target's native configuration type before AddUser.
// Several native managers type-assert accounts, so a wrong family is unsafe.
// One bounded listing plus one mutation share admission/deadline. No removal or
// replay; native duplicate/upsert semantics remain the selected protocol's own.
func (c *Adapter) AddInboundUser(ctx context.Context, addition domain.InboundUserAddition) (domain.UserMutationAcknowledgement, error) {
	if err := ctx.Err(); err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	defer done()
	user, expectedType, err := nativeUser(addition.User)
	if err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	operation, err := encodeUserOperation(&native.AddUserOperation{User: user})
	if err != nil {
		return domain.UserMutationAcknowledgement{}, errors.Join(domain.ErrInvalidUserAccount, err)
	}
	client, err := c.API.HandlerClient()
	if err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	listing, err := client.ListInbounds(ctx, &native.ListInboundsRequest{IsOnlyTags: false})
	if err != nil || ctx.Err() != nil {
		var cause error
		switch status.Code(err) {
		case codes.Unimplemented:
			cause = domain.ErrUserManagementUnsupported
		case codes.ResourceExhausted:
			cause = domain.ErrAPIResourceLimit
		case codes.Canceled:
			cause = context.Canceled
		case codes.DeadlineExceeded:
			cause = context.DeadlineExceeded
		}
		return domain.UserMutationAcknowledgement{}, fmt.Errorf("inspect Xray inbound before addition: %w", errors.Join(cause, ctx.Err(), err))
	}
	if listing == nil {
		return domain.UserMutationAcknowledgement{}, domain.ErrInvalidInboundResponse
	}
	matches := 0
	for _, inbound := range listing.Inbounds {
		if inbound == nil {
			return domain.UserMutationAcknowledgement{}, domain.ErrInvalidInboundResponse
		}
		if inbound.Tag == addition.InboundTag {
			matches++
			if inbound.ProxySettings == nil {
				return domain.UserMutationAcknowledgement{}, domain.ErrInvalidInboundResponse
			}
			if inbound.ProxySettings.Type != expectedType {
				return domain.UserMutationAcknowledgement{}, domain.ErrInboundProtocolMismatch
			}
		}
	}
	if matches == 0 {
		return domain.UserMutationAcknowledgement{}, domain.ErrInboundNotFound
	}
	if matches != 1 {
		return domain.UserMutationAcknowledgement{}, domain.ErrInvalidInboundResponse
	}
	if err := ctx.Err(); err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	_, err = client.AlterInbound(ctx, &native.AlterInboundRequest{Tag: addition.InboundTag, Operation: operation})
	if err != nil {
		return domain.UserMutationAcknowledgement{}, fmt.Errorf("add Xray inbound user: %w", userMutationCause(ctx, err))
	}
	return domain.UserMutationAcknowledgement{AcknowledgedAt: time.Now().UTC()}, nil
}
