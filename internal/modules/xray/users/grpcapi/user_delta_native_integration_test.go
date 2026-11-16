//go:build integration && linux

package grpcapi_test

import (
	"bytes"
	"context"
	"testing"

	native "github.com/xtls/xray-core/app/proxyman/command"

	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
	userapi "github.com/PapilioVPN/node/internal/modules/xray/users/infra/api"
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
