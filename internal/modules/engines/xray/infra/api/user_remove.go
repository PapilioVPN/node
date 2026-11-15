package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// RemoveInboundUser submits one exact AlterInbound operation, with no read/scan,
// persistence, application retry or claim that existing connections were closed.
func (c *Client) RemoveInboundUser(ctx context.Context, selection domain.InboundUserSelection) (domain.UserMutationAcknowledgement, error) {
	if err := ctx.Err(); err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return domain.UserMutationAcknowledgement{}, domain.ErrAPIBusy
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	operation := &native.RemoveUserOperation{Email: selection.Email}
	// Native ToTypedMessage discards marshal errors; retain them before dispatch.
	payload, err := proto.Marshal(operation)
	if err != nil {
		return domain.UserMutationAcknowledgement{}, errors.Join(domain.ErrInvalidUserSelection, err)
	}
	_, client, err := c.nativeClients()
	if err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.UserMutationAcknowledgement{}, err
	}
	_, err = client.AlterInbound(ctx, &native.AlterInboundRequest{Tag: selection.InboundTag, Operation: &serial.TypedMessage{Type: serial.GetMessageType(operation), Value: payload}})
	if err != nil {
		cause := domain.ErrUserMutationUnknown
		switch status.Code(err) {
		case codes.Unimplemented:
			cause = domain.ErrUserManagementUnsupported
		case codes.ResourceExhausted:
			cause = errors.Join(cause, domain.ErrAPIResourceLimit)
		case codes.Canceled:
			cause = errors.Join(cause, context.Canceled)
		case codes.DeadlineExceeded:
			cause = errors.Join(cause, context.DeadlineExceeded)
		}
		return domain.UserMutationAcknowledgement{}, fmt.Errorf("remove Xray inbound user: %w", errors.Join(cause, ctx.Err(), err))
	}
	return domain.UserMutationAcknowledgement{AcknowledgedAt: time.Now().UTC()}, nil
}
