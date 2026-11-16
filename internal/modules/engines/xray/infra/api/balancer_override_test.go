package api

import (
	"context"
	"testing"

	native "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

type completingBalancerClient struct {
	native.RoutingServiceClient
	complete func() (*native.OverrideBalancerTargetResponse, error)
}

func (c completingBalancerClient) OverrideBalancerTarget(context.Context, *native.OverrideBalancerTargetRequest, ...grpc.CallOption) (*native.OverrideBalancerTargetResponse, error) {
	return c.complete()
}

// Controlled SDK completion covers a cancellation race that cannot be timed
// deterministically over the wire. Native-service tests cover actual wire loss.
func TestBalancerOverrideRetainsReceivedAcknowledgementAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &Adapter{API: completingRoutingAPI{client: completingBalancerClient{complete: func() (*native.OverrideBalancerTargetResponse, error) {
		cancel()
		return &native.OverrideBalancerTargetResponse{}, nil
	}}}}
	ack, err := client.OverrideBalancerTarget(ctx, domain.BalancerOverride{BalancerTag: "pool"})
	if err != nil || ack.AcknowledgedAt.IsZero() {
		t.Fatalf("late cancellation erased confirmed clear: %+v %v", ack, err)
	}
}
