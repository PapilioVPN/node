package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// ReadInboundUserCount makes one native count request, not a user/inbound listing.
// Some native managers scan their own members to count; O(1) here describes only
// Node's calls/result space, not a hard bound on engine work or snapshot consistency.
func (c *Client) ReadInboundUserCount(ctx context.Context, tag string) (domain.InboundUserCount, error) {
	if err := ctx.Err(); err != nil {
		return domain.InboundUserCount{}, err
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return domain.InboundUserCount{}, domain.ErrAPIBusy
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	_, client, err := c.nativeClients()
	if err != nil {
		return domain.InboundUserCount{}, err
	}
	response, err := client.GetInboundUsersCount(ctx, &native.GetInboundUserRequest{Tag: tag})
	if cause := ctx.Err(); cause != nil {
		return domain.InboundUserCount{}, errors.Join(cause, err)
	}
	if err != nil {
		var cause error
		switch status.Code(err) {
		case codes.NotFound:
			cause = domain.ErrInboundNotFound
		case codes.Unimplemented:
			cause = domain.ErrUserReadUnsupported
		case codes.ResourceExhausted:
			cause = domain.ErrAPIResourceLimit
		case codes.Canceled:
			cause = context.Canceled
		case codes.DeadlineExceeded:
			cause = context.DeadlineExceeded
		}
		return domain.InboundUserCount{}, fmt.Errorf("read Xray inbound user count: %w", errors.Join(cause, err))
	}
	if response == nil || response.Count < 0 {
		return domain.InboundUserCount{}, domain.ErrInvalidUserCount
	}
	return domain.InboundUserCount{Count: response.Count, ObservedAt: time.Now().UTC()}, nil
}
