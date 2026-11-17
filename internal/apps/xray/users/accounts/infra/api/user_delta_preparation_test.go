package api_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xtls/xray-core/common/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
)

func TestUserDeltaRejectsCompleteInvalidPlansBeforeAnyNativeEffect(t *testing.T) {
	for _, mode := range []string{"uuid", "utf8", "mixed", "empty-email", "empty-tag", "duplicate", "overlap", "unknown-family", "malformed-member", "duplicate-member", "fold-unselectable"} {
		t.Run(mode, func(t *testing.T) {
			input := domain.InboundUserDelta{InboundTag: "in", Users: []domain.InboundUser{deltaUser("first"), deltaUser("second")}}
			client := &deltaClient{}
			cause := domain.ErrInvalidUserDelta
			switch mode {
			case "uuid":
				input.Users[1].Account.VLESS.ID = strings.Repeat("invalid", 10)
				cause = domain.ErrInvalidUserAccount
			case "utf8":
				input.Users[1].Email = string([]byte{0xff})
				cause = domain.ErrInvalidUserAccount
			case "mixed":
				input.Users[1].Account = domain.UserAccount{Protocol: domain.Trojan, Trojan: &domain.PasswordAccount{Password: "test-only"}}
				cause = domain.ErrInboundProtocolMismatch
			case "empty-email":
				input.RemoveEmails = []string{""}
				cause = domain.ErrInvalidUserSelection
			case "empty-tag":
				input.InboundTag = ""
				cause = domain.ErrInvalidInboundTag
			case "duplicate":
				input.Users[1].Email = "FIRST"
			case "overlap":
				input.RemoveEmails = []string{"FIRST"}
			case "unknown-family":
				input.Users = nil
				input.RemoveEmails = []string{"first"}
				client.typeName = "unknown.native.Config"
				cause = domain.ErrUserManagementUnsupported
			case "malformed-member":
				client.members = []*protocol.User{nil}
				cause = domain.ErrInvalidUserObservation
			case "duplicate-member":
				client.members = []*protocol.User{{Email: "first"}, {Email: "FIRST"}}
				cause = domain.ErrInvalidUserObservation
			case "fold-unselectable":
				input.Users = nil
				input.RemoveEmails = []string{"İ"}
				client.typeName = "xray.proxy.shadowsocks.ServerConfig"
			}
			adapter := &api.Adapter{API: additionsAPI{client: client}}
			report, err := adapter.ApplyInboundUserDelta(t.Context(), input)
			if !errors.Is(err, cause) || errors.Is(err, domain.ErrUserMutationUnknown) || len(report.Steps) != 0 || !report.ObservedAt.IsZero() {
				t.Fatalf("invalid delta escaped preparation: %v", err)
			}
			for _, call := range client.calls {
				if strings.HasPrefix(call, "add:") || strings.HasPrefix(call, "remove:") {
					t.Fatal("invalid trailing input removed an existing membership")
				}
			}
			client.typeName = ""
			client.members = nil
			if _, err := adapter.ApplyInboundUserDelta(t.Context(), domain.InboundUserDelta{InboundTag: "in", Users: []domain.InboundUser{deltaUser("valid")}}); err != nil {
				t.Fatal("failed preparation retained write admission")
			}
		})
	}
}

func TestUserDeltaKeepsNativeEmailMatchingAndDuplicateMemberships(t *testing.T) {
	for _, tc := range []struct {
		name, kind, query string
		members           []string
		removals          int
	}{
		{"lowercase", "xray.proxy.vless.inbound.Config", "selected", []string{"SELECTED", "other"}, 1},
		{"hysteria-exact", "xray.proxy.hysteria.ServerConfig", "selected", []string{"SELECTED", "selected", "selected"}, 2},
		{"wireguard-exact", "xray.proxy.wireguard.DeviceConfig", "selected", []string{"SELECTED", "selected", "selected"}, 2},
		{"wireguard-distinct", "xray.proxy.wireguard.DeviceConfig", "selected", []string{"SELECTED"}, 0},
		{"shadowsocks-fold", "xray.proxy.shadowsocks.ServerConfig", "σ", []string{"Σ", "ς", "other"}, 2},
		{"shadowsocks-kelvin", "xray.proxy.shadowsocks.ServerConfig", "k", []string{"K", "K", "k"}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &deltaClient{typeName: tc.kind, members: make([]*protocol.User, len(tc.members))}
			for i, email := range tc.members {
				client.members[i] = &protocol.User{Email: email}
			}
			report, err := (&api.Adapter{API: additionsAPI{client: client}}).ApplyInboundUserDelta(t.Context(), domain.InboundUserDelta{InboundTag: "in", RemoveEmails: []string{tc.query}})
			if err != nil || report.ObservedAt.IsZero() {
				t.Fatalf("native identity policy failed: %v", err)
			}
			if tc.removals == 0 {
				if len(report.Steps) != 1 || report.Steps[0].State != domain.UserMutationNotRequired || len(client.calls) != 2 {
					t.Fatal("absent exact identity became an acknowledged mutation")
				}
				return
			}
			if len(report.Steps) != tc.removals || len(client.calls) != 2+tc.removals {
				t.Fatal("native duplicate memberships were discarded or over-removed")
			}
			for _, step := range report.Steps {
				if step.Email != tc.query || step.State != domain.UserMutationAcknowledged || step.Action != domain.UserMutationRemove {
					t.Fatal("delta changed opaque identity bytes or effect reports")
				}
			}
		})
	}
}

func TestUserDeltaKeepsLateCompletionAndStopsBeforeTheNextDispatch(t *testing.T) {
	for _, trailing := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		client := &deltaClient{members: []*protocol.User{{Email: "selected"}}, mutate: func(int) error { cancel(); return nil }}
		input := domain.InboundUserDelta{InboundTag: "in", RemoveEmails: []string{"selected"}}
		if trailing {
			input.Users = []domain.InboundUser{deltaUser("next")}
		}
		report, err := (&api.Adapter{API: additionsAPI{client: client}}).ApplyInboundUserDelta(ctx, input)
		if len(report.Steps) < 1 || report.Steps[0].State != domain.UserMutationAcknowledged || report.Steps[0].AcknowledgedAt.IsZero() {
			t.Fatal("late cancellation erased an acknowledged native removal")
		}
		if !trailing && err != nil {
			t.Fatal("completed delta failed after its final acknowledgement")
		}
		if trailing && (!errors.Is(err, context.Canceled) || errors.Is(err, domain.ErrUserMutationUnknown) || report.Steps[1].State != domain.UserMutationNotApplied || len(client.calls) != 3) {
			t.Fatal("delta dispatched after cancellation or lost known no-effect state")
		}
		cancel()
	}
}

func TestUserDeltaUnsupportedLaterMutationCannotEraseEarlierEffects(t *testing.T) {
	client := &deltaClient{members: []*protocol.User{{Email: "selected"}}, mutate: func(call int) error {
		if call == 2 {
			return status.Error(codes.Unimplemented, "private backend failure")
		}
		return nil
	}}
	report, err := (&api.Adapter{API: additionsAPI{client: client}}).ApplyInboundUserDelta(t.Context(), domain.InboundUserDelta{InboundTag: "in", Users: []domain.InboundUser{deltaUser("selected")}})
	if !errors.Is(err, domain.ErrUserManagementUnsupported) || errors.Is(err, domain.ErrUserMutationUnknown) || len(report.Steps) != 2 || report.Steps[0].State != domain.UserMutationAcknowledged || report.Steps[1].State != domain.UserMutationNotApplied {
		t.Fatal("unsupported addition hid an acknowledged removal")
	}
}

func TestEmptyUserDeltaNeedsNoNativeOwnerAndPreservesCancellation(t *testing.T) {
	adapter := &api.Adapter{}
	if report, err := adapter.ApplyInboundUserDelta(t.Context(), domain.InboundUserDelta{}); err != nil || report.Steps == nil || len(report.Steps) != 0 || !report.ObservedAt.IsZero() {
		t.Fatal("empty delta acquired native work or claimed an observation")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := adapter.ApplyInboundUserDelta(ctx, domain.InboundUserDelta{}); !errors.Is(err, context.Canceled) {
		t.Fatal("empty delta hid caller cancellation")
	}
}
