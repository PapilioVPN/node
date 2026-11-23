// Package native speaks the pinned Xray RoutingService protocol.
package native

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/routing/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	listMethod     = "/xray.app.router.command.RoutingService/ListRule"
	balancerMethod = "/xray.app.router.command.RoutingService/GetBalancerInfo"
)

var (
	listOptions     = []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(4 << 20)}
	balancerOptions = []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(256 << 10)}
)

type instances interface {
	NativeConnection(string) (*grpc.ClientConn, error)
}

type Client struct {
	instances instances
	mutation  sync.Mutex
	fenced    *grpc.ClientConn
}

func New(instances instances) *Client { return &Client{instances: instances} }

func (client *Client) ListRoutingRules(ctx context.Context, id string) ([]state.Rule, error) {
	var response ListRuleResponse
	if err := client.invoke(ctx, id, listMethod, &emptypb.Empty{}, &response, listOptions); err != nil {
		return nil, err
	}
	rules := make([]state.Rule, len(response.Rules))
	for i, rule := range response.Rules {
		rules[i] = state.Rule{RuleTag: rule.GetRuleTag(), OutboundTag: rule.GetOutboundTag()}
	}
	return rules, nil
}

func (client *Client) GetBalancerInfo(ctx context.Context, id, tag string) (state.BalancerInfo, error) {
	var response GetBalancerInfoResponse
	if err := client.invoke(ctx, id, balancerMethod, &GetBalancerInfoRequest{Tag: tag}, &response, balancerOptions); err != nil {
		nativeStatus, ok := status.FromError(err)
		// The pinned router exposes a missing tag as an untyped gRPC error.
		// Recognize that exact error at this boundary only.
		if ok && nativeStatus.Code() == codes.Unknown && nativeStatus.Message() == "app/router: cannot find tag" {
			return state.BalancerInfo{}, errors.Join(state.ErrBalancerNotFound, err)
		}
		return state.BalancerInfo{}, err
	}
	balancer := response.GetBalancer()
	var info state.BalancerInfo
	if override := balancer.GetOverride(); override != nil {
		info.OverrideTarget = &override.Target
	}
	if targets := balancer.GetPrincipleTarget(); targets != nil {
		info.ReportedTargets = targets.GetTags()
		if info.ReportedTargets == nil {
			info.ReportedTargets = []string{}
		}
	}
	return info, nil
}

// The pinned handlers do not use RPC deadlines to interrupt routing work.
// Keep the client limit and stream cancellation without a server timer.
type localDeadlineContext struct{ context.Context }

func (localDeadlineContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (client *Client) invoke(ctx context.Context, id, method string, request, response any, options []grpc.CallOption) error {
	conn, err := client.instances.NativeConnection(id)
	if err != nil {
		return err
	}
	err = conn.Invoke(localDeadlineContext{ctx}, method, request, response, options...)
	if cause := ctx.Err(); cause != nil {
		return cause
	}
	current, stateErr := client.instances.NativeConnection(id)
	if stateErr != nil {
		return errors.Join(stateErr, err)
	}
	if current != conn {
		return errors.Join(state.ErrUnavailable, err)
	}
	if status.Code(err) == codes.ResourceExhausted {
		return errors.Join(state.ErrTooLarge, err)
	}
	return err
}
