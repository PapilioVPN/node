package grpc

import (
	"testing"

	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/xray/logs/live"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetStatusMapsInstanceIdentity(t *testing.T) {
	h := handlers{service: lifecycle.New(lifecycle.Config{ID: "primary"}, live.New("primary"))}
	if _, err := h.GetStatus(t.Context(), &instancesv1.XrayInstanceRequest{Id: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing instance error = %v, want NotFound", err)
	}
	got, err := h.GetStatus(t.Context(), &instancesv1.XrayInstanceRequest{Id: "primary"})
	if err != nil || got.GetId() != "primary" || got.GetState() != instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_STOPPED {
		t.Fatalf("GetStatus = (%v, %v), want primary stopped", got, err)
	}
}
