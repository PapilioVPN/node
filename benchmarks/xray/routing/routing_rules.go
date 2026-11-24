package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/PapilioVPN/node/benchmarks/xray/internal/fixture"

	routingv1 "github.com/PapilioVPN/node/internal/engines/xray/routing/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	appendOp       = "append"
	replaceOp      = "replace"
	removeOp       = "remove"
	removeAbsentOp = "remove-absent"
	benchRuleTag   = "benchmark-rule"
	absentRuleTag  = "absent-fixture-rule"

	appendRuleSource = `{"type":"field","ruleTag":"benchmark-rule","port":"8443","outboundTag":"direct"}`
	// Keep the reference's native management inbound routable after replacement.
	replaceRuleSource = `{"type":"field","ruleTag":"benchmark-rule","inboundTag":["API_INBOUND"],"outboundTag":"API"}`
)

type ruleChanges struct {
	appendRule  func(context.Context) error
	replaceRule func(context.Context) error
	removeRule  func(context.Context, string) error
}

func routingRuleHTTP(client *http.Client, address string, reference bool) ruleChanges {
	if reference {
		base := address + "/routing/rules"
		add := func(source string, replace bool) func(context.Context) error {
			payload := referenceRule(source, replace).ProtoReflect().GetUnknown()
			return func(ctx context.Context) error {
				return routingHTTPRead(ctx, client, http.MethodPut, base, payload, &emptypb.Empty{}, false)
			}
		}
		remove := func(ctx context.Context, tag string) error {
			data := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), tag)
			return routingHTTPRead(ctx, client, http.MethodDelete, base, data, &emptypb.Empty{}, false)
		}
		return ruleChanges{add(appendRuleSource, false), add(replaceRuleSource, true), remove}
	}

	base := address + "/v1/xray/instances/primary/routing/rules"
	add := func(source, mode string, confirmed bool) func(context.Context) error {
		body, err := json.Marshal(struct {
			Rule           json.RawMessage `json:"rule"`
			Mode           string          `json:"mode"`
			ConfirmReplace bool            `json:"confirm_replace"`
		}{json.RawMessage(source), mode, confirmed})
		fixture.Check(err)
		return func(ctx context.Context) error {
			return routingHTTPRead(ctx, client, http.MethodPost, base, body, nil, true)
		}
	}
	remove := func(ctx context.Context, tag string) error {
		return routingHTTPRead(ctx, client, http.MethodDelete, base+"?tag="+url.QueryEscape(tag), nil, nil, true)
	}
	return ruleChanges{add(appendRuleSource, "append", false), add(replaceRuleSource, "replace", true), remove}
}

func routingRuleGRPC(conn *grpc.ClientConn, reference bool) ruleChanges {
	if reference {
		const service = "/service.NodeService/"
		add := func(source string, replace bool) func(context.Context) error {
			req := referenceRule(source, replace)
			return func(ctx context.Context) error {
				return conn.Invoke(routingKey(ctx), service+"AddRoutingRule", req, &emptypb.Empty{}, grpc.StaticMethod())
			}
		}
		remove := func(ctx context.Context, tag string) error {
			return conn.Invoke(routingKey(ctx), service+"RemoveRoutingRule", wrapperspb.String(tag), &emptypb.Empty{}, grpc.StaticMethod())
		}
		return ruleChanges{add(appendRuleSource, false), add(replaceRuleSource, true), remove}
	}
	client := routingv1.NewXrayRoutingServiceClient(conn)
	add := func(source string, mode routingv1.XrayRoutingRuleMode, confirmed bool) func(context.Context) error {
		req := &routingv1.XrayAddRoutingRuleRequest{
			Id: "primary", RuleJson: []byte(source), Mode: mode, ConfirmReplace: confirmed,
		}
		return func(ctx context.Context) error {
			_, err := client.AddRoutingRule(routingKey(ctx), req)
			return err
		}
	}
	remove := func(ctx context.Context, tag string) error {
		_, err := client.RemoveRoutingRule(routingKey(ctx), &routingv1.XrayRemoveRoutingRuleRequest{
			Id: "primary", RuleTag: tag,
		})
		return err
	}
	return ruleChanges{
		add(appendRuleSource, routingv1.XrayRoutingRuleMode_XRAY_ROUTING_RULE_MODE_APPEND, false),
		add(replaceRuleSource, routingv1.XrayRoutingRuleMode_XRAY_ROUTING_RULE_MODE_REPLACE, true),
		remove,
	}
}

// PasarGuard's AddRoutingRuleRequest at 739eab9 uses a JSON string and a
// should_reset boolean. The Node request confirms replace explicitly.
func referenceRule(source string, replace bool) *emptypb.Empty {
	encoded := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), source)
	if replace {
		encoded = protowire.AppendVarint(protowire.AppendTag(encoded, 2, protowire.VarintType), 1)
	}
	message := &emptypb.Empty{}
	message.ProtoReflect().SetUnknown(encoded)
	return message
}

func IsRuleWrite(operation string) bool {
	switch operation {
	case appendOp, replaceOp, removeOp, removeAbsentOp:
		return true
	default:
		return false
	}
}

func sampleRuleChanges(ctx context.Context, calls ruleChanges, operation string, window time.Duration) (fixture.PollResult, error) {
	var sample func(context.Context) error
	var prepare, restore func(context.Context) error
	remove := func(ctx context.Context) error { return calls.removeRule(ctx, benchRuleTag) }

	switch operation {
	case appendOp:
		sample, restore = calls.appendRule, remove
	case replaceOp:
		sample = calls.replaceRule
	case removeOp:
		prepare, sample = calls.appendRule, remove
	case removeAbsentOp:
		sample = func(ctx context.Context) error { return calls.removeRule(ctx, absentRuleTag) }
	default:
		return fixture.PollResult{}, fmt.Errorf("invalid rule write %q", operation)
	}

	var measurements fixture.PollResult
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) || len(measurements.Timings) == 0 {
		if prepare != nil {
			if err := prepare(ctx); err != nil {
				return fixture.PollResult{}, fmt.Errorf("prepare rule: %w", err)
			}
		}
		started := time.Now()
		err := sample(ctx)
		elapsed := time.Since(started)
		if err != nil {
			return fixture.PollResult{}, err
		}
		measurements.Timings = append(measurements.Timings, elapsed)
		measurements.Elapsed += elapsed
		if restore != nil {
			if err := restore(ctx); err != nil {
				return fixture.PollResult{}, fmt.Errorf("restore rules: %w", err)
			}
		}
	}
	return measurements, nil
}
