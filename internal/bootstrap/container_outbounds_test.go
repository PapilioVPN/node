//go:build container

package bootstrap_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	outboundsv1 "github.com/PapilioVPN/node/internal/apps/xray/network/outbounds/grpcapi/proto/v1"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
)

func verifyContainerOutbounds(t *testing.T, ctx context.Context, client xrayv1.XrayServiceClient) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := client.ListOutboundStatuses(ctx, &outboundsv1.ListOutboundStatusesRequest{InstanceId: "packaged-xray"})
		if err != nil && status.Code(err) != codes.Unavailable {
			t.Fatalf("packaged outbound observation failed: %v", err)
		}
		if err == nil && len(result.Statuses) > 0 {
			if result.InstanceId != "packaged-xray" || len(result.Statuses) != 1 || result.ObservedAtUnixMs <= 0 {
				t.Fatal("packaged outbound collection changed native membership/time")
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("packaged native observatory did not report")
		case <-ticker.C:
		}
	}
	// The native observer uses the explicitly selected blackhole outbound; no
	// external HTTP target or host routing is involved in this actual failure.
	value, err := client.GetOutboundStatus(ctx, &outboundsv1.GetOutboundStatusRequest{InstanceId: "packaged-xray", OutboundTag: proto.String("blocked")})
	if err != nil || value.InstanceId != "packaged-xray" || value.OutboundTag != "blocked" || value.Status == nil || value.Status.OutboundTag != "blocked" || value.Status.Alive || value.Status.DelayMs != 99999999 || value.Status.LastSeenUnixSec != 0 || value.Status.LastTryUnixSec <= 0 || value.Status.HealthPing != nil || value.ObservedAtUnixMs <= 0 {
		t.Fatalf("packaged native failed-probe observation changed: %v %v", value, err)
	}
	missing, err := client.GetOutboundStatus(ctx, &outboundsv1.GetOutboundStatusRequest{InstanceId: "packaged-xray", OutboundTag: proto.String("missing")})
	if err != nil || missing.Status != nil || missing.OutboundTag != "missing" || missing.ObservedAtUnixMs <= 0 {
		t.Fatal("packaged absence became successful zero delay")
	}
	if _, err := client.GetOutboundStatus(ctx, &outboundsv1.GetOutboundStatusRequest{InstanceId: "packaged-xray"}); status.Code(err) != codes.InvalidArgument {
		t.Fatal("packaged omitted tag selected anonymous observation")
	}
	if _, err := client.ListOutboundStatuses(ctx, &outboundsv1.ListOutboundStatusesRequest{InstanceId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatal("packaged collection grant or instance selection changed")
	}
}
