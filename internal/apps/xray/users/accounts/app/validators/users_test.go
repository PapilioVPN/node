package validators_test

import (
	"bytes"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

func TestTypedAccountsRequireExactlyOneMatchingRecordAndKeepOpaqueCredentials(t *testing.T) {
	for _, account := range []domain.UserAccount{
		{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "id", Flow: "xtls-rprx-vision"}},
		{Protocol: domain.VMess, VMess: &domain.VMessAccount{ID: "id"}},
		{Protocol: domain.Trojan, Trojan: &domain.PasswordAccount{Password: " password "}},
		{Protocol: domain.Shadowsocks, Shadowsocks: &domain.ShadowsocksAccount{Password: " password ", Cipher: domain.AES128GCM}},
		{Protocol: domain.Shadowsocks2022, Shadowsocks2022: &domain.Shadowsocks2022Account{Key: bytes.Repeat([]byte{7}, 16)}},
		{Protocol: domain.Hysteria, Hysteria: &domain.HysteriaAccount{Auth: " auth "}},
		{Protocol: domain.MASQUE, MASQUE: &domain.PasswordAccount{Password: " password "}},
		{Protocol: domain.WireGuard, WireGuard: &domain.WireGuardAccount{PublicKey: bytes.Repeat([]byte{7}, 32), PreSharedKey: bytes.Repeat([]byte{8}, 32), AllowedIPs: []string{"10.1.0.2/32", "fd00::2/128"}}},
	} {
		addition := domain.InboundUserAddition{InboundTag: " exact ", User: domain.InboundUser{Email: " Mixed@Example ", Level: 42, Account: account}}
		if err := validators.InboundUserAddition(addition); err != nil {
			t.Fatalf("protocol %s rejected: %v", account.Protocol, err)
		}
	}
	tooLarge := uint32(65536)
	for _, account := range []domain.UserAccount{
		{}, {Protocol: domain.VLESS, Trojan: &domain.PasswordAccount{Password: "password"}},
		{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "id"}, Trojan: &domain.PasswordAccount{Password: "password"}},
		{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: ""}},
		{Protocol: domain.Trojan, Trojan: &domain.PasswordAccount{Password: "\xff"}},
		{Protocol: domain.Shadowsocks, Shadowsocks: &domain.ShadowsocksAccount{Password: "p", Cipher: "unexpected"}},
		{Protocol: domain.Shadowsocks2022, Shadowsocks2022: &domain.Shadowsocks2022Account{Key: []byte{1}}},
		{Protocol: domain.WireGuard, WireGuard: &domain.WireGuardAccount{PublicKey: bytes.Repeat([]byte{1}, 32), AllowedIPs: []string{"bad"}}},
		{Protocol: domain.WireGuard, WireGuard: &domain.WireGuardAccount{PublicKey: bytes.Repeat([]byte{1}, 32), AllowedIPs: []string{"10.0.0.2/32"}, KeepAliveSeconds: &tooLarge}},
	} {
		if err := validators.InboundUserAddition(domain.InboundUserAddition{InboundTag: "in", User: domain.InboundUser{Email: "user", Account: account}}); err == nil {
			t.Fatalf("invalid protocol/account was accepted: %s", account.Protocol)
		}
	}
}
