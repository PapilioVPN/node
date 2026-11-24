package grpc

import (
	"testing"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
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

func TestStartRejectsInvalidCredentialEnvelope(t *testing.T) {
	h := handlers{service: lifecycle.New(lifecycle.Config{ID: "primary"}, live.New("primary"))}
	for _, tc := range []struct {
		name    string
		account *instancesv1.XrayInitialAccount
	}{
		{"mixed legacy and typed credential", &instancesv1.XrayInitialAccount{Email: "mixed", Uuid: "d5120cd4-15b3-4ca7-a198-0a32d269c135",
			Credential: &accountsv1.Credential{Protocol: "trojan", Password: "fixture"}}},
		{"WireGuard keep-alive overflow", &instancesv1.XrayInitialAccount{Email: "overflow", Credential: &accountsv1.Credential{
			Protocol: "wireguard", Peer: &accountsv1.WireGuardPeer{KeepAlive: 65536}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.Start(t.Context(), &instancesv1.XrayInstanceRequest{Id: "primary", InitialUsers: []*instancesv1.XrayInitialInboundUsers{
				{InboundTag: "fixture", Accounts: []*instancesv1.XrayInitialAccount{tc.account}},
			}})
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("Start = %v, want InvalidArgument", err)
			}
			current, err := h.service.GetStatus("primary")
			if err != nil || current.State != lifecycle.Stopped {
				t.Fatalf("state = (%+v, %v), want stopped", current, err)
			}
		})
	}
}
