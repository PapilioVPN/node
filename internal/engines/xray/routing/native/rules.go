package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/PapilioVPN/node/internal/engines/xray/routing/state"
	routercmd "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/infra/conf"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

const (
	addRuleMethod    = routercmd.RoutingService_AddRule_FullMethodName
	removeRuleMethod = routercmd.RoutingService_RemoveRule_FullMethodName
)

func (c *Client) AddRoutingRule(ctx context.Context, id string, source []byte, appendRule bool) error {
	return c.withMutation(ctx, id, func(conn *grpc.ClientConn) error {
		request, ruleTag, balancingTag, err := compileRule(source, appendRule)
		if err != nil {
			return err
		}
		current, err := c.instances.NativeConnection(id)
		if err != nil {
			return err
		}
		if current != conn {
			return state.ErrUnavailable
		}
		return c.sendMutation(ctx, id, conn, addRuleMethod, request, func(err error) error {
			switch {
			case ruleTag != "" && exactNativeError(err, "app/router: duplicate ruleTag "+ruleTag):
				return state.ErrRuleExists
			case balancingTag != "" && exactNativeError(err, "app/router: balancer "+balancingTag+" not found"):
				return state.ErrBalancerNotFound
			case exactNativeError(err, "app/router: this rule has no effective fields"):
				return state.ErrInvalid
			}
			return nil
		})
	})
}

func (c *Client) RemoveRoutingRule(ctx context.Context, id, tag string) error {
	return c.mutate(ctx, id, removeRuleMethod, &routercmd.RemoveRuleRequest{RuleTag: tag}, nil)
}

func compileRule(source []byte, appendRule bool) (*routercmd.AddRuleRequest, string, string, error) {
	config := &conf.RouterConfig{RuleList: []json.RawMessage{source}}
	routing, err := config.Build()
	if err != nil {
		return nil, "", "", errors.Join(state.ErrInvalid, err)
	}
	rule := routing.GetRule()[0]
	if len(rule.GetRuleTag()) > state.MaxTagBytes {
		return nil, "", "", state.ErrInvalid
	}
	for _, expression := range rule.GetAttributes() {
		// Xray uses regexp.MustCompile for attributes when installing the rule.
		if _, err := regexp.Compile(expression); err != nil {
			return nil, "", "", errors.Join(state.ErrInvalid, err)
		}
	}
	if !appendRule && rule.GetBalancingTag() != "" {
		return nil, "", "", fmt.Errorf("%w: replace clears the balancer selected by this rule", state.ErrInvalid)
	}
	payload, err := proto.Marshal(routing)
	if err != nil {
		return nil, "", "", errors.Join(state.ErrInvalid, err)
	}
	if len(payload) > 1<<20 {
		return nil, "", "", state.ErrTooLarge
	}
	request := &routercmd.AddRuleRequest{
		Config: &serial.TypedMessage{
			Type:  "xray.app.router.Config",
			Value: payload,
		},
		ShouldAppend: appendRule,
	}
	return request, rule.GetRuleTag(), rule.GetBalancingTag(), nil
}
