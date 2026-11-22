package grpcapi

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/app/services"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/tools/assets/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
	"testing"
	"time"
)

func TestRequiredRevisionAndOperationalReceipt(t *testing.T) {
	s, err := services.New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{Service: s, Timeout: time.Second}
	if _, err := h.ActivateAssets(context.Background(), &v1.ActivateRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.StageAssets(context.Background(), &v1.StageRequest{Candidate: &v1.Candidate{}}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	rev := uint64(0)
	result, err := h.ActivateAssets(context.Background(), &v1.ActivateRequest{InstanceId: "one", ExpectedRevision: &rev, ExecutionId: strings.Repeat("a", 32), ReleaseId: strings.Repeat("b", 64)})
	if err != nil || result.ErrorCode != "not_found" || result.Outcome != "not_applied" || result.CompletedAt != nil {
		t.Fatal(result, err)
	}
}
