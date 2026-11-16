package api

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	native "github.com/xtls/xray-core/app/router/command"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// RemoveRoutingRule invokes one native RemoveRule without a membership preflight,
// configuration rewrite or retry. Native missing tags are acknowledged no-ops.
func (c *Client) RemoveRoutingRule(ctx context.Context, tag string) (domain.RoutingMutationAcknowledgement, error) {
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
	if !utf8.ValidString(tag) {
		return domain.RoutingMutationAcknowledgement{}, domain.ErrInvalidRoutingRuleTag
	}
	client, err := c.routingClient()
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
