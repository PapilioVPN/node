package api

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	native "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// OverrideBalancerTarget performs one live assignment. Empty clears the override;
// no membership preflight, strategy reset, config rewrite or retry is added.
func (c *Client) OverrideBalancerTarget(ctx context.Context, selection domain.BalancerOverride) (domain.RoutingMutationAcknowledgement, error) {
	if err := ctx.Err(); err != nil {
		return domain.RoutingMutationAcknowledgement{}, err
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return domain.RoutingMutationAcknowledgement{}, domain.ErrAPIBusy
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if !utf8.ValidString(selection.BalancerTag) || !utf8.ValidString(selection.Target) {
		return domain.RoutingMutationAcknowledgement{}, domain.ErrInvalidBalancerOverride
	}
	client, err := c.routingClient()
	if err != nil {
		return domain.RoutingMutationAcknowledgement{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.RoutingMutationAcknowledgement{}, err
	}
	_, err = client.OverrideBalancerTarget(ctx, &native.OverrideBalancerTargetRequest{BalancerTag: selection.BalancerTag, Target: selection.Target})
	if err != nil {
		return domain.RoutingMutationAcknowledgement{}, fmt.Errorf("override Xray balancer target: %w", routingMutationCause(ctx, err))
	}
	// Receipt of native completion survives a later cancellation race.
	return domain.RoutingMutationAcknowledgement{AcknowledgedAt: time.Now().UTC()}, nil
}

func routingMutationCause(ctx context.Context, err error) error {
	cause := domain.ErrRoutingMutationUnknown
	switch status.Code(err) {
	case codes.Unimplemented:
		cause = domain.ErrRoutingManagementUnsupported
	case codes.ResourceExhausted:
		cause = errors.Join(cause, domain.ErrAPIResourceLimit)
	case codes.Canceled:
		cause = errors.Join(cause, context.Canceled)
	case codes.DeadlineExceeded:
		cause = errors.Join(cause, context.DeadlineExceeded)
	}
	return errors.Join(cause, ctx.Err(), err)
}
