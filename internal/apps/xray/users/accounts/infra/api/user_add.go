package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/accounts"
)

// AddInboundUser checks the target's native configuration type before AddUser.
// Several native managers type-assert accounts, so a wrong family is unsafe.
// One bounded listing plus one mutation share admission/deadline. No removal or
// replay; native duplicate/upsert semantics remain the selected protocol's own.
func (c *Adapter) AddInboundUser(ctx context.Context, addition domain.InboundUserAddition) (domain.UserMutationAcknowledgement, error) {
	if err := ctx.Err(); err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	ctx, done, err := c.beginMutation(ctx)
	if err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	defer done()
	user, expectedType, err := accounts.EncodeUser(addition.User)
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
	if _, err := userInboundType(ctx, client, addition.InboundTag, expectedType); err != nil {
		return domain.UserMutationAcknowledgement{}, err
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

// userInboundType uses the pinned API's complete listing because no targeted
// tag/type read exists. An empty expected type is a removal-only delta lookup.
func userInboundType(ctx context.Context, client native.HandlerServiceClient, tag, expectedType string) (string, error) {
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
		return "", fmt.Errorf("inspect Xray inbound before mutation: %w", errors.Join(cause, ctx.Err(), err))
	}
	if listing == nil {
		return "", domain.ErrInvalidInboundResponse
	}
	matches := 0
	var observedType string
	for _, inbound := range listing.Inbounds {
		if inbound == nil {
			return "", domain.ErrInvalidInboundResponse
		}
		if inbound.Tag == tag {
			matches++
			if inbound.ProxySettings == nil || expectedType == "" && inbound.ProxySettings.Type == "" {
				return "", domain.ErrInvalidInboundResponse
			}
			observedType = inbound.ProxySettings.Type
			if expectedType != "" && observedType != expectedType {
				return "", domain.ErrInboundProtocolMismatch
			}
		}
	}
	if matches == 0 {
		return "", domain.ErrInboundNotFound
	}
	if matches != 1 {
		return "", domain.ErrInvalidInboundResponse
	}
	return observedType, nil
}
