package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	native "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

func (c *Client) routingClient() (native.RoutingServiceClient, error) {
	if _, _, err := c.nativeClients(); err != nil {
		return nil, err
	}
	// Native clients are initialized once under the same lock and never
	// reassigned. They share one channel, admission/receive bounds and Close owner.
	return c.routing, nil
}

// ReadRoutingRules reads native ordered rule references, not their conditions.
// Complete within the receive ceiling or fail; the native API has no pagination.
func (c *Client) ReadRoutingRules(ctx context.Context) (domain.RoutingRules, error) {
	if err := ctx.Err(); err != nil {
		return domain.RoutingRules{}, err
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return domain.RoutingRules{}, domain.ErrAPIBusy
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	client, err := c.routingClient()
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
func (c *Client) ReadBalancerInfo(ctx context.Context, tag string) (domain.BalancerInfo, error) {
	if err := ctx.Err(); err != nil {
		return domain.BalancerInfo{}, err
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return domain.BalancerInfo{}, domain.ErrAPIBusy
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	client, err := c.routingClient()
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
