package api

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	native "github.com/xtls/xray-core/app/router/command"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
)

// RemoveRoutingRule invokes one native RemoveRule without a membership preflight,
// configuration rewrite or retry. Native missing tags are acknowledged no-ops.
func (c *Router) RemoveRoutingRule(ctx context.Context, tag string) (domain.RoutingMutationAcknowledgement, error) {
	if err := ctx.Err(); err != nil {
		return domain.RoutingMutationAcknowledgement{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.RoutingMutationAcknowledgement{}, err
	}
	defer done()
	if !utf8.ValidString(tag) {
		return domain.RoutingMutationAcknowledgement{}, domain.ErrInvalidRoutingRuleTag
	}
	client, err := c.API.RoutingClient()
	if err != nil {
		return domain.RoutingMutationAcknowledgement{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.RoutingMutationAcknowledgement{}, err
	}
	_, err = client.RemoveRule(ctx, &native.RemoveRuleRequest{RuleTag: tag})
	if err != nil {
		return domain.RoutingMutationAcknowledgement{}, fmt.Errorf("remove Xray routing rule: %w", routingMutationCause(ctx, err))
	}
	return domain.RoutingMutationAcknowledgement{AcknowledgedAt: time.Now().UTC()}, nil
}
