package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	native "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
)

// ReadRoutingRules reads native ordered rule references, not their conditions.
// Complete within the receive ceiling or fail; the native API has no pagination.
func (c *Router) ReadRoutingRules(ctx context.Context) (domain.RoutingRules, error) {
	if err := ctx.Err(); err != nil {
		return domain.RoutingRules{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.RoutingRules{}, err
	}
	defer done()
	client, err := c.API.RoutingClient()
	if err != nil {
		return domain.RoutingRules{}, err
	}
	response, err := client.ListRule(ctx, &native.ListRuleRequest{})
	if cause := ctx.Err(); cause != nil {
		return domain.RoutingRules{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.RoutingRules{}, routingReadError(err)
	}
	if response == nil {
		return domain.RoutingRules{}, domain.ErrInvalidRoutingResponse
	}
	result := domain.RoutingRules{Items: make([]domain.RoutingRuleReference, 0, len(response.Rules))}
	for _, rule := range response.Rules {
		if err := ctx.Err(); err != nil {
			return domain.RoutingRules{}, err
		}
		if rule == nil {
			return domain.RoutingRules{}, domain.ErrInvalidRoutingResponse
		}
		result.Items = append(result.Items, domain.RoutingRuleReference{OutboundTag: rule.Tag, RuleTag: rule.RuleTag})
	}
	if err := ctx.Err(); err != nil {
		return domain.RoutingRules{}, err
	}
	result.ObservedAt = time.Now().UTC()
	return result, nil
}

// ReadBalancerInfo does one targeted native lookup. Missing submessages remain
// unavailable, distinct from reported empty values. Native principal-target errors
// can be suppressed upstream, so this is not a complete configuration/health view.
func (c *Router) ReadBalancerInfo(ctx context.Context, tag string) (domain.BalancerInfo, error) {
	if err := ctx.Err(); err != nil {
		return domain.BalancerInfo{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.BalancerInfo{}, err
	}
	defer done()
	client, err := c.API.RoutingClient()
	if err != nil {
		return domain.BalancerInfo{}, err
	}
	response, err := client.GetBalancerInfo(ctx, &native.GetBalancerInfoRequest{Tag: tag})
	if cause := ctx.Err(); cause != nil {
		return domain.BalancerInfo{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.BalancerInfo{}, routingReadError(err)
	}
	value := response.GetBalancer()
	if value == nil {
		return domain.BalancerInfo{}, domain.ErrInvalidRoutingResponse
	}
	result := domain.BalancerInfo{ObservedAt: time.Now().UTC()}
	if value.Override != nil {
		target := value.Override.Target
		result.OverrideTarget = &target
	}
	if value.PrincipleTarget != nil {
		result.PrincipalTargets = &domain.BalancerTargets{Tags: value.PrincipleTarget.Tag}
	}
	return result, nil
}

func routingReadError(err error) error {
	var cause error
	switch status.Code(err) {
	case codes.NotFound:
		cause = domain.ErrRoutingTargetNotFound
	case codes.Unimplemented:
		cause = domain.ErrRoutingReadUnsupported
	case codes.ResourceExhausted:
		cause = domain.ErrAPIResourceLimit
	case codes.Canceled:
		cause = context.Canceled
	case codes.DeadlineExceeded:
		cause = context.DeadlineExceeded
	}
	return fmt.Errorf("read Xray routing observation: %w", errors.Join(cause, err))
}
