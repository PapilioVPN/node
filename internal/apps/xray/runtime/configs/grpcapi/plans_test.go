package grpcapi

import (
	"context"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

func TestReviewedRPCsRequireMessagesAndExplicitRevision(t *testing.T) {
	h := Handler{}
	ctx := context.Background()
	if _, err := h.PlanConfiguration(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.PlanConfiguration(ctx, &v1.PlanConfigurationRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.ApplyConfigurationPlan(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.GetConfigurationApplication(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.RecoverConfiguration(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.RecoverConfiguration(ctx, &v1.RecoverConfigurationRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}
