package api

import (
	"context"
	"fmt"
	"time"

	router "github.com/xtls/xray-core/app/router"
	native "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// ApplyRoutingRule sends one prepared router.Config through the existing channel.
// It adds no membership preflight, retry, configuration rewrite or compensation.
func (c *Adapter) ApplyRoutingRule(ctx context.Context, rule domain.PreparedRoutingRule, mode domain.RoutingRuleMode) (domain.RoutingMutationAcknowledgement, error) {
	if err := ctx.Err(); err != nil {
		return domain.RoutingMutationAcknowledgement{}, err
	}
	if mode != domain.AppendRoutingRule && mode != domain.ReplaceRoutingRules {
		return domain.RoutingMutationAcknowledgement{}, domain.ErrInvalidRoutingRuleMode
	}
	if len(rule.Config) == 0 {
		return domain.RoutingMutationAcknowledgement{}, domain.ErrInvalidPreparedRoutingRule
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.RoutingMutationAcknowledgement{}, err
	}
	defer done()
	client, err := c.API.RoutingClient()
	if err != nil {
		return domain.RoutingMutationAcknowledgement{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.RoutingMutationAcknowledgement{}, err
	}
	_, err = client.AddRule(ctx, &native.AddRuleRequest{
		Config:       &serial.TypedMessage{Type: string(proto.MessageName(&router.Config{})), Value: rule.Config},
		ShouldAppend: mode == domain.AppendRoutingRule,
	})
	if err != nil {
		return domain.RoutingMutationAcknowledgement{}, fmt.Errorf("apply Xray routing rule: %w", routingMutationCause(ctx, err))
	}
	return domain.RoutingMutationAcknowledgement{AcknowledgedAt: time.Now().UTC()}, nil
}
