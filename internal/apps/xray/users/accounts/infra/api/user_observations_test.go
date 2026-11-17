package api

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math"
	"testing"

	proxyman "github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	hysteria "github.com/xtls/xray-core/proxy/hysteria/account"
	"github.com/xtls/xray-core/proxy/masque"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	ss2022 "github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vmess"
	"github.com/xtls/xray-core/proxy/wireguard"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

func TestUserObservationRetainsCompleteNativeAccountsIncludingExtensions(t *testing.T) {
	for _, message := range []proto.Message{
		&vless.Account{Id: "8e781ad6-dae6-4ed4-9d7b-8c247f735869", Flow: "xtls-rprx-vision", Encryption: "native-encryption", XorMode: 2, Seconds: math.MaxUint32, Padding: "native-padding", Reverse: &vless.Reverse{Tag: "reverse", Sniffing: &proxyman.SniffingConfig{Enabled: true, DestinationOverride: []string{"http", "tls"}, MetadataOnly: true, RouteOnly: true}}, Testpre: math.MaxUint32, Testseed: []uint32{1, math.MaxUint32}},
		&vmess.Account{Id: "8e781ad6-dae6-4ed4-9d7b-8c247f735869", SecuritySettings: &protocol.SecurityConfig{Type: protocol.SecurityType_AES128_GCM}, TestsEnabled: "AuthenticatedLength|NoTerminationSignal"},
		&trojan.Account{Password: " opaque password "},
		&shadowsocks.Account{Password: " opaque password ", CipherType: shadowsocks.CipherType_XCHACHA20_POLY1305, IvCheck: true},
		&ss2022.Account{Key: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))},
		&hysteria.Account{Auth: " opaque auth "},
		&masque.Account{Password: " opaque password "},
		&wireguard.PeerConfig{PublicKey: hex.EncodeToString(bytes.Repeat([]byte{7}, 32)), PreSharedKey: hex.EncodeToString(bytes.Repeat([]byte{8}, 32)), Endpoint: "native-peer:1234", KeepAlive: "0", AllowedIps: []string{"10.0.0.2/32", "fd00::2/128"}},
	} {
		t.Run(serial.GetMessageType(message), func(t *testing.T) {
			payload, err := proto.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			// Unknown native account fields must survive a control-plane SDK lag.
			payload = protowire.AppendTag(payload, 200, protowire.VarintType)
			payload = protowire.AppendVarint(payload, 42)
			user := &protocol.User{Email: " Mixed@Example ", Level: math.MaxUint32, Account: &serial.TypedMessage{Type: serial.GetMessageType(message), Value: payload}}
			result, err := observedUser(user)
			if err != nil || result.Email != user.Email || result.Level != math.MaxUint32 || result.Account.TypeName != user.Account.Type || !bytes.Equal(result.Account.Protobuf, payload) {
				t.Fatal("native identity/precision/account fields changed; credentials withheld")
			}
		})
	}
}

func TestUserObservationRejectsUnreadableRecordsWithoutReturningPartialCredentials(t *testing.T) {
	for _, user := range []*protocol.User{
		nil, {}, {Email: "user"},
		{Email: "user", Account: &serial.TypedMessage{Type: "xray.proxy.vless.Account", Value: []byte{0xff}}},
		{Email: "user", Account: &serial.TypedMessage{Type: "xray.proxy.vless.Account", Value: []byte{0x0a, 0x01, 0xff}}},
		{Email: "user", Account: &serial.TypedMessage{Type: "xray.proxy.vless.Reverse"}},
	} {
		result, err := observedUser(user)
		if !errors.Is(err, domain.ErrInvalidUserObservation) || result.Email != "" || result.Level != 0 || result.Account.TypeName != "" || len(result.Account.Protobuf) != 0 {
			t.Fatal("invalid account became partial/successful facts")
		}
	}
	for _, user := range []*protocol.User{
		{Email: "user", Account: &serial.TypedMessage{Type: "unknown.future.account"}},
		{Email: "user", Account: &serial.TypedMessage{Type: "xray.proxy.trojan.Account"}},
	} {
		if user.Account.Type == "xray.proxy.trojan.Account" {
			user.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 200, protowire.VarintType), 1))
		}
		if _, err := observedUser(user); !errors.Is(err, domain.ErrUserRecordUnsupported) {
			t.Fatal("unrepresented native user/unknown account fields were silently discarded")
		}
	}
}
