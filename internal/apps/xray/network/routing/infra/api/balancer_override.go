package api

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	native "github.com/xtls/xray-core/app/router/command"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
)

// OverrideBalancerTarget performs one live assignment. Empty clears the override;
// no membership preflight, strategy reset, config rewrite or retry is added.
func (c *Router) OverrideBalancerTarget(ctx context.Context, selection domain.BalancerOverride) (domain.RoutingMutationAcknowledgement, error) {
	if err := ctx.Err(); err != nil {
		return domain.RoutingMutationAcknowledgement{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.RoutingMutationAcknowledgement{}, err
	}
	defer done()
	if !utf8.ValidString(selection.BalancerTag) || !utf8.ValidString(selection.Target) {
		return domain.RoutingMutationAcknowledgement{}, domain.ErrInvalidBalancerOverride
	}
	client, err := c.API.RoutingClient()
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
