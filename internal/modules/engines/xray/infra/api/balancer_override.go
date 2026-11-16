package api

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	native "github.com/xtls/xray-core/app/router/command"

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
