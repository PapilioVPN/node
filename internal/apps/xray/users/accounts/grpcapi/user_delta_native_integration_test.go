//go:build integration && linux

package grpcapi_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"testing"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	userv1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	userapi "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
)

// Reuse the single all-family engine fixture; credentials remain synthetic and
// private. This checks real membership, not all eight data-plane handshakes.
func verifyNativeUserDelta(t *testing.T, ctx context.Context, adapter *userapi.Adapter, nativeClient native.HandlerServiceClient, tag string, baseline int64) {
	t.Helper()
	accounts := map[string]domain.UserAccount{
		"vless":       {Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "delta-original"}},
		"vmess":       {Protocol: domain.VMess, VMess: &domain.VMessAccount{ID: "delta-original"}},
		"trojan":      {Protocol: domain.Trojan, Trojan: &domain.PasswordAccount{Password: "delta-original"}},
		"shadowsocks": {Protocol: domain.Shadowsocks, Shadowsocks: &domain.ShadowsocksAccount{Password: "delta-original", Cipher: domain.AES128GCM, IVCheck: true}},
		"ss2022":      {Protocol: domain.Shadowsocks2022, Shadowsocks2022: &domain.Shadowsocks2022Account{Key: bytes.Repeat([]byte{12}, 16)}},
		"hysteria":    {Protocol: domain.Hysteria, Hysteria: &domain.HysteriaAccount{Auth: "delta-original"}},
		"masque":      {Protocol: domain.MASQUE, MASQUE: &domain.PasswordAccount{Password: "delta-original"}},
		"wireguard":   {Protocol: domain.WireGuard, WireGuard: &domain.WireGuardAccount{PublicKey: bytes.Repeat([]byte{3}, 32), PreSharedKey: bytes.Repeat([]byte{8}, 32), AllowedIPs: []string{"10.254.2.6/32"}}},
	}
	user := domain.InboundUser{Email: "delta@local", Level: 23, Account: accounts[tag]}
	if _, err := adapter.AddInboundUser(ctx, domain.InboundUserAddition{InboundTag: tag, User: user}); err != nil {
		t.Fatalf("native delta seed failed: %v", err)
	}
	removals := 1
	switch tag {
	case "shadowsocks":
		user.Account.Shadowsocks.Password += "-duplicate"
	case "hysteria":
		user.Account.Hysteria.Auth += "-duplicate"
	case "wireguard":
		user.Account.WireGuard.PublicKey[0] ^= 1
		user.Account.WireGuard.AllowedIPs = []string{"10.254.2.7/32"}
	}
	if tag == "shadowsocks" || tag == "hysteria" || tag == "wireguard" {
		if _, err := adapter.AddInboundUser(ctx, domain.InboundUserAddition{InboundTag: tag, User: user}); err != nil {
			t.Fatal("native duplicate-email delta fixture failed")
		}
		removals = 2
	}
	// Remove-before-add also applies when lossy native readback would look equal.
	// Clearing PSK uses a removed WireGuard peer, never a blind upsert assumption.
	if tag == "wireguard" {
		user.Account.WireGuard.PublicKey[0] ^= 2
		user.Account.WireGuard.PreSharedKey = nil
		user.Account.WireGuard.AllowedIPs = []string{"10.254.2.8/32"}
	}
	user.Level = ^uint32(0)
	report, err := adapter.ApplyInboundUserDelta(ctx, domain.InboundUserDelta{InboundTag: tag, RemoveEmails: []string{"absent-delta@local"}, Users: []domain.InboundUser{user}})
	if err != nil || report.ObservedAt.IsZero() || len(report.Steps) != removals+2 || report.Steps[0].State != domain.UserMutationNotRequired {
		t.Fatalf("real native delta failed: %v", err)
	}
	for i, step := range report.Steps[1:] {
		action := domain.UserMutationRemove
		if i == removals {
			action = domain.UserMutationAdd
		}
		if step.Email != user.Email || step.Action != action || step.State != domain.UserMutationAcknowledged || step.AcknowledgedAt.IsZero() {
			t.Fatal("real delta lost ordered native receipts")
		}
	}
	records, err := nativeClient.GetInboundUsers(ctx, &native.GetInboundUserRequest{Tag: tag})
	if err != nil || int64(len(records.GetUsers())) != baseline+1 {
		t.Fatal("real delta lost unrelated memberships or retained old duplicate records")
	}
	selected := 0
	for _, member := range records.Users {
		if member.Email == user.Email {
			selected++
			if member.Level != user.Level {
				t.Fatal("delta lost native uint32 level")
			}
		}
	}
	if selected != 1 {
		t.Fatal("real delta did not replace every selected native membership")
	}
	removed, err := adapter.ApplyInboundUserDelta(ctx, domain.InboundUserDelta{InboundTag: tag, RemoveEmails: []string{user.Email}})
	if err != nil || len(removed.Steps) != 1 || removed.Steps[0].State != domain.UserMutationAcknowledged {
		t.Fatal("real removal delta did not acknowledge its effect")
	}
	count, err := nativeClient.GetInboundUsersCount(ctx, &native.GetInboundUserRequest{Tag: tag})
	if err != nil || count.GetCount() != baseline {
		t.Fatal("native delta cleanup changed unrelated membership")
	}
}

func verifyPublicUserDelta(t *testing.T, ctx context.Context, client xrayv1.XrayServiceClient, tag string, template *userv1.InboundUser, baseline int64) {
	t.Helper()
	user := proto.Clone(template).(*userv1.InboundUser)
	user.Email = "public-delta@local"
	if _, err := client.AddInboundUser(ctx, &userv1.AddInboundUserRequest{InstanceId: "edge", InboundTag: tag, User: user}); err != nil {
		t.Fatal("public delta seed failed")
	}
	rejected, err := client.ApplyInboundUserDelta(ctx, &userv1.ApplyInboundUserDeltaRequest{InstanceId: "edge", InboundTag: tag, Users: []*userv1.InboundUser{user}, RemoveEmails: []string{user.Email}})
	if rejected != nil || status.Code(err) != codes.InvalidArgument {
		t.Fatal("conflicting delta identities reached native effects")
	}
	if details := status.Convert(err).Details(); len(details) != 1 {
		t.Fatal("invalid delta lost known no-effect detail")
	} else if failure, ok := details[0].(*userv1.UserDeltaFailure); !ok || failure.Outcome != userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_NOT_APPLIED || failure.Progress.ObservedAtUnixMs != nil || len(failure.Progress.Steps) != 0 {
		t.Fatal("rejected delta fabricated a native observation or effect")
	}
	user.Level = ^uint32(0)
	result, err := client.ApplyInboundUserDelta(ctx, &userv1.ApplyInboundUserDeltaRequest{InstanceId: "edge", InboundTag: tag, Users: []*userv1.InboundUser{user}, RemoveEmails: []string{"absent-public-delta@local"}})
	if err != nil || result.InstanceId != "edge" || result.InboundTag != tag || result.ObservedAtUnixMs == nil || len(result.Steps) != 3 || result.Steps[0].State != userv1.UserDeltaState_USER_DELTA_STATE_NOT_REQUIRED || result.Steps[0].AcknowledgedAtUnixMs != nil {
		t.Fatalf("public native delta failed: %v", err)
	}
	for i, step := range result.Steps[1:] {
		action := userv1.UserDeltaAction_USER_DELTA_ACTION_REMOVE
		if i == 1 {
			action = userv1.UserDeltaAction_USER_DELTA_ACTION_ADD
		}
		if step.Email != user.Email || step.Action != action || step.State != userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED || step.AcknowledgedAtUnixMs == nil {
			t.Fatal("public delta lost native effect receipts")
		}
	}
	stored, err := client.GetInboundUserMetadata(ctx, &userv1.GetInboundUserMetadataRequest{InstanceId: "edge", InboundTag: tag, Email: user.Email})
	if err != nil || stored.User == nil || stored.User.Level != user.Level {
		t.Fatal("public delta did not update actual native identity/level")
	}
	expectedRemoval := userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED
	if tag == "wireguard" {
		// A well-shaped key can still be invalid for this running device. Native
		// rejection follows the acknowledged removal and must stay visibly partial.
		key, err := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{9}, 32))
		if err != nil {
			t.Fatal(err)
		}
		invalid := proto.Clone(user).(*userv1.InboundUser)
		invalid.GetWireguard().PublicKey = key.PublicKey().Bytes()
		trailing := proto.Clone(user).(*userv1.InboundUser)
		trailing.Email = "trailing-public-delta@local"
		trailing.GetWireguard().PublicKey = bytes.Repeat([]byte{11}, 32)
		response, err := client.ApplyInboundUserDelta(ctx, &userv1.ApplyInboundUserDeltaRequest{InstanceId: "edge", InboundTag: tag, Users: []*userv1.InboundUser{invalid, trailing}})
		if response != nil || status.Code(err) != codes.Unavailable {
			t.Fatal("real native delta failure became successful replacement")
		}
		details := status.Convert(err).Details()
		if len(details) != 1 {
			t.Fatal("real partial delta lost progress")
		}
		failure, ok := details[0].(*userv1.UserDeltaFailure)
		if !ok || failure.Outcome != userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN || len(failure.Progress.Steps) != 3 || failure.Progress.Steps[0].State != userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED || failure.Progress.Steps[1].State != userv1.UserDeltaState_USER_DELTA_STATE_UNKNOWN || failure.Progress.Steps[2].State != userv1.UserDeltaState_USER_DELTA_STATE_NOT_ATTEMPTED {
			t.Fatal("real native delta hid an earlier removal or attempted its trailing addition")
		}
		expectedRemoval = userv1.UserDeltaState_USER_DELTA_STATE_NOT_REQUIRED
	}
	cleanup, err := client.ApplyInboundUserDelta(ctx, &userv1.ApplyInboundUserDeltaRequest{InstanceId: "edge", InboundTag: tag, RemoveEmails: []string{user.Email}})
	if err != nil || len(cleanup.Steps) != 1 || cleanup.Steps[0].State != expectedRemoval {
		t.Fatal("public delta cleanup lost actual membership")
	}
	count, err := client.GetInboundUserCount(ctx, &userv1.GetInboundUserCountRequest{InstanceId: "edge", InboundTag: tag})
	if err != nil || count.Count != baseline {
		t.Fatal("public delta changed unrelated native membership")
	}
}
