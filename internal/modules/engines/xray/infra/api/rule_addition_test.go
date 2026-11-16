package api

import (
	"context"
	"testing"

	native "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

type completingRuleAdditionClient struct {
	native.RoutingServiceClient
	complete func() (*native.AddRuleResponse, error)
}

func (c completingRuleAdditionClient) AddRule(context.Context, *native.AddRuleRequest, ...grpc.CallOption) (*native.AddRuleResponse, error) {
	return c.complete()
}

func TestRuleAdditionRetainsReceivedAcknowledgementAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &Adapter{API: completingRoutingAPI{client: completingRuleAdditionClient{complete: func() (*native.AddRuleResponse, error) { cancel(); return &native.AddRuleResponse{}, nil }}}}
	ack, err := client.ApplyRoutingRule(ctx, domain.PreparedRoutingRule{Config: []byte{1}}, domain.ReplaceRoutingRules)
	if err != nil || ack.AcknowledgedAt.IsZero() {
		t.Fatalf("late cancellation erased acknowledged replacement: %+v %v", ack, err)
	}
}
