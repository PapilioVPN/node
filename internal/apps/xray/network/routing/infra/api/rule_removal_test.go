package api

import (
	"context"
	"testing"

	native "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc"
)

type completingRuleRemovalClient struct {
	native.RoutingServiceClient
	complete func() (*native.RemoveRuleResponse, error)
}

func (c completingRuleRemovalClient) RemoveRule(context.Context, *native.RemoveRuleRequest, ...grpc.CallOption) (*native.RemoveRuleResponse, error) {
	return c.complete()
}

// Controlled SDK completion deterministically exercises the late cancellation race.
func TestRuleRemovalRetainsReceivedAcknowledgementAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &Router{API: completingRoutingAPI{client: completingRuleRemovalClient{complete: func() (*native.RemoveRuleResponse, error) { cancel(); return &native.RemoveRuleResponse{}, nil }}}}
	ack, err := client.RemoveRoutingRule(ctx, "rule")
	if err != nil || ack.AcknowledgedAt.IsZero() {
		t.Fatalf("late cancellation erased received removal acknowledgement: %+v %v", ack, err)
	}
}
