package api

import (
	"context"
	"testing"
	"time"

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
	client.routing = completingBalancerClient{complete: func() (*native.OverrideBalancerTargetResponse, error) {
		cancel()
		return &native.OverrideBalancerTargetResponse{}, nil
	}}
	ack, err := client.OverrideBalancerTarget(ctx, domain.BalancerOverride{BalancerTag: "pool"})
	if err != nil || ack.AcknowledgedAt.IsZero() {
		t.Fatalf("late cancellation erased confirmed clear: %+v %v", ack, err)
	}
}
