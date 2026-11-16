package api

import (
	"context"
	"testing"
	"time"

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
	client, err := NewClient(Config{Network: "unix", Address: "/unused/api.sock", RequestTimeout: time.Second, MaxReceiveBytes: 4096, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.routingClient(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client.routing = completingRuleRemovalClient{complete: func() (*native.RemoveRuleResponse, error) { cancel(); return &native.RemoveRuleResponse{}, nil }}
	ack, err := client.RemoveRoutingRule(ctx, "rule")
	if err != nil || ack.AcknowledgedAt.IsZero() {
		t.Fatalf("late cancellation erased received removal acknowledgement: %+v %v", ack, err)
	}
}
