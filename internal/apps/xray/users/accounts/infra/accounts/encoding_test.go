package accounts

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math"
	"testing"

	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/uuid"
	hysteria "github.com/xtls/xray-core/proxy/hysteria/account"
	"github.com/xtls/xray-core/proxy/masque"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	ss2022 "github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vmess"
	"github.com/xtls/xray-core/proxy/wireguard"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

func TestAccountEncodingPreservesEveryBaselineFamily(t *testing.T) {
	id := "8e781ad6-dae6-4ed4-9d7b-8c247f735869"
	key, psk := bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)
	zero := uint32(0)
	cases := []struct {
		account domain.UserAccount
		message proto.Message
		inbound string
	}{
		{domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: id, Flow: "xtls-rprx-vision"}}, &vless.Account{Id: id, Flow: "xtls-rprx-vision"}, "xray.proxy.vless.inbound.Config"},
		{domain.UserAccount{Protocol: domain.VMess, VMess: &domain.VMessAccount{ID: id}}, &vmess.Account{Id: id}, "xray.proxy.vmess.inbound.Config"},
		{domain.UserAccount{Protocol: domain.Trojan, Trojan: &domain.PasswordAccount{Password: " password "}}, &trojan.Account{Password: " password "}, serial.GetMessageType(&trojan.ServerConfig{})},
		{domain.UserAccount{Protocol: domain.Shadowsocks, Shadowsocks: &domain.ShadowsocksAccount{Password: " password ", Cipher: domain.AES128GCM, IVCheck: true}}, &shadowsocks.Account{Password: " password ", CipherType: shadowsocks.CipherType_AES_128_GCM, IvCheck: true}, serial.GetMessageType(&shadowsocks.ServerConfig{})},
		{domain.UserAccount{Protocol: domain.Shadowsocks2022, Shadowsocks2022: &domain.Shadowsocks2022Account{Key: key}}, &ss2022.Account{Key: base64.StdEncoding.EncodeToString(key)}, serial.GetMessageType(&ss2022.MultiUserServerConfig{})},
		{domain.UserAccount{Protocol: domain.Hysteria, Hysteria: &domain.HysteriaAccount{Auth: " auth "}}, &hysteria.Account{Auth: " auth "}, "xray.proxy.hysteria.ServerConfig"},
		{domain.UserAccount{Protocol: domain.MASQUE, MASQUE: &domain.PasswordAccount{Password: " password "}}, &masque.Account{Password: " password "}, serial.GetMessageType(&masque.ServerConfig{})},
		{domain.UserAccount{Protocol: domain.WireGuard, WireGuard: &domain.WireGuardAccount{PublicKey: key, PreSharedKey: psk, AllowedIPs: []string{"10.0.0.2/32", "fd00::2/128"}, KeepAliveSeconds: &zero}}, &wireguard.PeerConfig{PublicKey: hex.EncodeToString(key), PreSharedKey: hex.EncodeToString(psk), AllowedIps: []string{"10.0.0.2/32", "fd00::2/128"}, KeepAlive: "0"}, serial.GetMessageType(&wireguard.DeviceConfig{})},
	}
	for _, tc := range cases {
		t.Run(string(tc.account.Protocol), func(t *testing.T) {
			user, inbound, err := EncodeUser(domain.InboundUser{Email: " Mixed@Example ", Level: math.MaxUint32, Account: tc.account})
			if err != nil || inbound != tc.inbound || user.Email != " Mixed@Example " || user.Level != math.MaxUint32 {
				t.Fatalf("native user identity/type changed: %v", err)
			}
			decoded, err := user.Account.GetInstance()
			if err != nil || !proto.Equal(decoded, tc.message) {
				t.Fatal("native account fields changed") // Never print credentials on failure.
			}
			if _, err := user.ToMemoryUser(); err != nil {
				t.Fatalf("pinned native parser rejected account: %v", err)
			}
		})
	}
}

func TestNativeUUIDFormsAndWireGuardOptionalKeepalive(t *testing.T) {
	for _, id := range []string{"short-id", "8e781ad6-dae6-4ed4-9d7b-8c247f735869"} {
		user, _, err := EncodeUser(domain.InboundUser{Account: domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: id}}})
		if err != nil {
			t.Fatal(err)
		}
		message, err := user.Account.GetInstance()
		expected, parseErr := uuid.ParseString(id)
		if err != nil || parseErr != nil || message.(*vless.Account).Id != expected.String() {
			t.Fatal("native UUID normalization changed")
		}
	}
	if _, _, err := EncodeUser(domain.InboundUser{Account: domain.UserAccount{Protocol: domain.VMess, VMess: &domain.VMessAccount{ID: "not-a-valid-native-uuid-with-more-than-thirty-characters"}}}); !errors.Is(err, domain.ErrInvalidUserAccount) {
		t.Fatal("invalid native UUID did not retain typed cause")
	}
	account := &domain.WireGuardAccount{PublicKey: bytes.Repeat([]byte{7}, 32), AllowedIPs: []string{"10.0.0.2/32"}}
	user, _, err := EncodeUser(domain.InboundUser{Account: domain.UserAccount{Protocol: domain.WireGuard, WireGuard: account}})
	if err != nil {
		t.Fatal(err)
	}
	message, err := user.Account.GetInstance()
	if err != nil || message.(*wireguard.PeerConfig).KeepAlive != "" || message.(*wireguard.PeerConfig).PreSharedKey != "" {
		t.Fatal("omitted WireGuard fields were synthesized")
	}
}

func TestEveryShadowsocksCipherHasAnExplicitNativeValue(t *testing.T) {
	for cipher, expected := range map[domain.ShadowsocksCipher]shadowsocks.CipherType{
		domain.AES128GCM: shadowsocks.CipherType_AES_128_GCM, domain.AES256GCM: shadowsocks.CipherType_AES_256_GCM,
		domain.ChaCha20Poly1305: shadowsocks.CipherType_CHACHA20_POLY1305, domain.XChaCha20Poly1305: shadowsocks.CipherType_XCHACHA20_POLY1305, domain.CipherNone: 9,
	} {
		user, _, err := EncodeUser(domain.InboundUser{Account: domain.UserAccount{Protocol: domain.Shadowsocks, Shadowsocks: &domain.ShadowsocksAccount{Password: "test-only", Cipher: cipher}}})
		if err != nil {
			t.Fatal(err)
		}
		message, err := user.Account.GetInstance()
		if err != nil || message.(*shadowsocks.Account).CipherType != expected {
			t.Fatal("cipher changed or fell back")
		}
		_, err = user.ToMemoryUser()
		if (err != nil) != (cipher == domain.CipherNone) {
			t.Fatal("pinned native cipher support changed; reassess compatibility")
		}
	}
}
