package grpcapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/ports"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/grpcapi"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/sync/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type controlled struct {
	ports.Operations
	calls    int
	deadline bool
}

func (c *controlled) Apply(ctx context.Context, id, plan string, r domain.Revision) (domain.ReceiptPage, error) {
	c.calls++
	_, c.deadline = ctx.Deadline()
	return domain.ReceiptPage{Receipt: domain.Receipt{InstanceID: id, OperationID: "op", Revision: r, Effect: accounts.Unknown, RequiresRecovery: true}}, errors.New("private credential-bearing native diagnostic")
}
func TestBoundaryRejectsMissingPresenceAndRetainsFormedErrors(t *testing.T) {
	c := &controlled{}
	h := grpcapi.Handler{Service: c, Timeout: time.Second}
	ctx := context.Background()
	if _, err := h.RecoverSync(ctx, &v1.RecoveryRequest{InstanceId: "one", OperationId: "op"}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.PreviewSync(ctx, &v1.SyncRequest{Expected: &v1.Revision{Epoch: "epoch"}, Mode: v1.Mode_FULL, Sets: []*v1.UserSet{{InboundTag: "selected"}}}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.ApplySync(ctx, &v1.ApplyRequest{InstanceId: "one", PlanId: "plan"}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if c.calls != 0 {
		t.Fatal("invalid envelopes reached the service")
	}
	r, err := h.ApplySync(ctx, &v1.ApplyRequest{InstanceId: "one", PlanId: "plan", Expected: &v1.Revision{Epoch: "epoch", Config: 1<<63 + 1}})
	if err != nil || r.Effect != "unknown" || !r.RequiresRecovery || r.Revision.Config != 1<<63+1 || r.Error != "unavailable" || strings.Contains(r.String(), "private") || !c.deadline {
		t.Fatal(r, err)
	}
}
