package configuration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	hysteria "github.com/xtls/xray-core/proxy/hysteria"
	"github.com/xtls/xray-core/proxy/masque"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	ss2022 "github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/trojan"
	vless "github.com/xtls/xray-core/proxy/vless/inbound"
	vmess "github.com/xtls/xray-core/proxy/vmess/inbound"
	"github.com/xtls/xray-core/proxy/wireguard"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/configuration"
)

func TestPreparationReplacesOnlySelectedUserCollections(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	zero := uint32(0)
	cases := []struct {
		name     string
		settings proto.Message
		account  domain.UserAccount
		users    func(proto.Message) []*protocol.User
		clear    func(proto.Message)
	}{
		{"vless", &vless.Config{Decryption: "none", XorMode: 1}, domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "test-user", Flow: "xtls-rprx-vision"}}, func(m proto.Message) []*protocol.User { return m.(*vless.Config).Users }, func(m proto.Message) { m.(*vless.Config).Users = nil }},
		{"vmess", &vmess.Config{Default: &vmess.DefaultConfig{Level: 7}}, domain.UserAccount{Protocol: domain.VMess, VMess: &domain.VMessAccount{ID: "test-user"}}, func(m proto.Message) []*protocol.User { return m.(*vmess.Config).User }, func(m proto.Message) { m.(*vmess.Config).User = nil }},
		{"trojan", &trojan.ServerConfig{Fallbacks: []*trojan.Fallback{{Dest: "8080"}}}, domain.UserAccount{Protocol: domain.Trojan, Trojan: &domain.PasswordAccount{Password: "test-only-password"}}, func(m proto.Message) []*protocol.User { return m.(*trojan.ServerConfig).Users }, func(m proto.Message) { m.(*trojan.ServerConfig).Users = nil }},
		{"shadowsocks", &shadowsocks.ServerConfig{}, domain.UserAccount{Protocol: domain.Shadowsocks, Shadowsocks: &domain.ShadowsocksAccount{Password: "test-only-password", Cipher: domain.AES128GCM, IVCheck: true}}, func(m proto.Message) []*protocol.User { return m.(*shadowsocks.ServerConfig).Users }, func(m proto.Message) { m.(*shadowsocks.ServerConfig).Users = nil }},
		{"ss2022", &ss2022.MultiUserServerConfig{Method: "2022-blake3-aes-256-gcm", Key: "server-key"}, domain.UserAccount{Protocol: domain.Shadowsocks2022, Shadowsocks2022: &domain.Shadowsocks2022Account{Key: key}}, func(m proto.Message) []*protocol.User { return m.(*ss2022.MultiUserServerConfig).Users }, func(m proto.Message) { m.(*ss2022.MultiUserServerConfig).Users = nil }},
		{"hysteria", &hysteria.ServerConfig{}, domain.UserAccount{Protocol: domain.Hysteria, Hysteria: &domain.HysteriaAccount{Auth: "test-only-auth"}}, func(m proto.Message) []*protocol.User { return m.(*hysteria.ServerConfig).Users }, func(m proto.Message) { m.(*hysteria.ServerConfig).Users = nil }},
		{"masque", &masque.ServerConfig{Address: []string{"10.0.0.1/24"}, Mtu: 1400}, domain.UserAccount{Protocol: domain.MASQUE, MASQUE: &domain.PasswordAccount{Password: "test-only-password"}}, func(m proto.Message) []*protocol.User { return m.(*masque.ServerConfig).Users }, func(m proto.Message) { m.(*masque.ServerConfig).Users = nil }},
		{"wireguard", &wireguard.DeviceConfig{SecretKey: "server-key", Endpoint: []string{"10.0.0.1/24"}, NoKernelTun: true, Mtu: 1400}, domain.UserAccount{Protocol: domain.WireGuard, WireGuard: &domain.WireGuardAccount{PublicKey: key, PreSharedKey: bytes.Repeat([]byte{8}, 32), AllowedIPs: []string{"10.0.0.2/32"}, KeepAliveSeconds: &zero}}, func(m proto.Message) []*protocol.User { return m.(*wireguard.DeviceConfig).Users }, func(m proto.Message) { m.(*wireguard.DeviceConfig).Users = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Unknown fields at every edited envelope must survive, too.
			tc.settings.ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 0x03})
			config := configurationFixture(tc.settings)
			original := marshal(t, config)
			preparer, err := configuration.NewPreparer(configuration.Limits{MaxBytes: 1 << 20, MaxUsers: 1000})
			if err != nil {
				t.Fatal(err)
			}
			input := []domain.InboundUserSet{{InboundTag: "selected", Users: []domain.InboundUser{{Email: " Second@Local ", Level: 7, Account: tc.account}, {Email: "first@local", Level: 2, Account: tc.account}}}}
			prepared, err := preparer.Prepare(t.Context(), original, input)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, marshal(t, config)) {
				t.Fatal("borrowed input changed")
			}
			var after core.Config
			if err := proto.Unmarshal(prepared, &after); err != nil {
				t.Fatal(err)
			}
			decoded := proto.Clone(tc.settings)
			if err := proto.Unmarshal(after.Inbound[0].ProxySettings.Value, decoded); err != nil {
				t.Fatal(err)
			}
			users := tc.users(decoded)
			if len(users) != 2 || users[0].Email != " Second@Local " || users[0].Level != 7 || users[1].Email != "first@local" || users[1].Level != 2 {
				t.Fatal("ordered identities changed")
			}
			for _, user := range users {
				if _, err := user.ToMemoryUser(); err != nil {
					t.Fatal("native account rejected")
				}
			}
			tc.clear(decoded)
			if !proto.Equal(tc.settings, decoded) {
				t.Fatal("non-user or unknown settings changed")
			}
			after.Inbound[0].ProxySettings.Value = config.Inbound[0].ProxySettings.Value
			if !proto.Equal(config, &after) {
				t.Fatal("unselected configuration changed")
			}
			// Clearing users must retain the user-capable native configuration type.
			cleared, err := preparer.Prepare(t.Context(), prepared, []domain.InboundUserSet{{InboundTag: "selected"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := proto.Unmarshal(cleared, &after); err != nil {
				t.Fatal(err)
			}
			if err := proto.Unmarshal(after.Inbound[0].ProxySettings.Value, decoded); err != nil {
				t.Fatal(err)
			}
			if len(tc.users(decoded)) != 0 || !proto.Equal(decoded, tc.settings) {
				t.Fatal("clear changed non-user settings")
			}
		})
	}
}

func TestPreparationRejectsBeforeReturningAnyCandidate(t *testing.T) {
	base := configurationFixture(&vless.Config{Decryption: "none"})
	valid := domain.InboundUser{Email: "test@local", Account: domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "test-user"}}}
	for _, tc := range []struct {
		name   string
		config *core.Config
		sets   []domain.InboundUserSet
		want   error
	}{
		{"missing", base, []domain.InboundUserSet{{InboundTag: "absent"}}, domain.ErrInboundNotFound},
		{"duplicate selection", base, []domain.InboundUserSet{{InboundTag: "selected"}, {InboundTag: "selected"}}, domain.ErrInvalidUserConfiguration},
		{"blank selection", base, []domain.InboundUserSet{{}}, domain.ErrInvalidInboundTag},
		{"wrong protocol", base, []domain.InboundUserSet{{InboundTag: "selected", Users: []domain.InboundUser{{Account: domain.UserAccount{Protocol: domain.Trojan, Trojan: &domain.PasswordAccount{Password: "test-only"}}}}}}, domain.ErrInboundProtocolMismatch},
		{"unsupported", configurationFixture(&trojan.Account{}), []domain.InboundUserSet{{InboundTag: "selected"}}, domain.ErrUserManagementUnsupported},
		{"client WireGuard", configurationFixture(&wireguard.DeviceConfig{IsClient: true}), []domain.InboundUserSet{{InboundTag: "selected"}}, domain.ErrUserManagementUnsupported},
		{"late missing", base, []domain.InboundUserSet{{InboundTag: "selected", Users: []domain.InboundUser{valid}}, {InboundTag: "absent"}}, domain.ErrInboundNotFound},
		{"mixed account families", base, []domain.InboundUserSet{{InboundTag: "selected", Users: []domain.InboundUser{valid, {Account: domain.UserAccount{Protocol: domain.Trojan, Trojan: &domain.PasswordAccount{Password: "test-only"}}}}}}, domain.ErrInboundProtocolMismatch},
		{"late encoding failure", base, []domain.InboundUserSet{{InboundTag: "selected", Users: []domain.InboundUser{valid, {Account: domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "not-a-valid-native-uuid-with-more-than-thirty-characters"}}}}}}, domain.ErrInvalidUserAccount},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := configuration.NewPreparer(configuration.Limits{MaxBytes: 1 << 20, MaxUsers: 1000})
			if err != nil {
				t.Fatal(err)
			}
			input := marshal(t, tc.config)
			before := bytes.Clone(input)
			result, err := p.Prepare(t.Context(), input, tc.sets)
			if !errors.Is(err, tc.want) || result != nil || !bytes.Equal(input, before) {
				t.Fatal("failed preparation returned a candidate, changed input or lost its cause")
			}
		})
	}
}

func TestPreparationLimitsCancellationAndCollectionSizes(t *testing.T) {
	config := configurationFixture(&vless.Config{Decryption: "none"})
	input := marshal(t, config)
	for _, count := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			users := make([]domain.InboundUser, count)
			for i := range users {
				users[i] = domain.InboundUser{Email: fmt.Sprintf("%d@local", i), Account: domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: fmt.Sprint(i)}}}
			}
			p, err := configuration.NewPreparer(configuration.Limits{MaxBytes: 1 << 20, MaxUsers: 1000})
			if err != nil {
				t.Fatal(err)
			}
			result, err := p.Prepare(t.Context(), input, []domain.InboundUserSet{{InboundTag: "selected", Users: users}})
			if err != nil {
				t.Fatal(err)
			}
			var compiled core.Config
			if err := proto.Unmarshal(result, &compiled); err != nil {
				t.Fatal(err)
			}
			var selected vless.Config
			if err := proto.Unmarshal(compiled.Inbound[0].ProxySettings.Value, &selected); err != nil {
				t.Fatal(err)
			}
			if len(selected.Users) != count {
				t.Fatal("collection cardinality changed")
			}
		})
	}
	p, _ := configuration.NewPreparer(configuration.Limits{MaxBytes: len(input), MaxUsers: 1})
	user := domain.InboundUser{Account: domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "test-user"}}}
	twoInbounds := proto.Clone(config).(*core.Config)
	twoInbounds.Inbound[1] = proto.Clone(twoInbounds.Inbound[0]).(*core.InboundHandlerConfig)
	twoInbounds.Inbound[1].Tag = "second"
	twoInput := marshal(t, twoInbounds)
	combined, _ := configuration.NewPreparer(configuration.Limits{MaxBytes: 1 << 20, MaxUsers: 1})
	result, err := combined.Prepare(t.Context(), twoInput, []domain.InboundUserSet{
		{InboundTag: "selected", Users: []domain.InboundUser{user}},
		{InboundTag: "second", Users: []domain.InboundUser{user}},
	})
	if result != nil || !errors.Is(err, domain.ErrUserCollectionTooLarge) {
		t.Fatal("user limit was applied independently per inbound instead of across the request")
	}
	for _, tc := range []struct {
		input []byte
		sets  []domain.InboundUserSet
		want  error
	}{
		{append(bytes.Clone(input), 0), nil, domain.ErrUserConfigurationTooLarge},
		{input, []domain.InboundUserSet{{InboundTag: "selected", Users: []domain.InboundUser{user}}}, domain.ErrUserConfigurationTooLarge},
		{input, []domain.InboundUserSet{{InboundTag: "selected", Users: []domain.InboundUser{user, user}}}, domain.ErrUserCollectionTooLarge},
		{[]byte{0xff}, nil, domain.ErrInvalidUserConfiguration},
		{nil, nil, domain.ErrInvalidUserConfiguration},
	} {
		out, err := p.Prepare(t.Context(), tc.input, tc.sets)
		if !errors.Is(err, tc.want) || out != nil {
			t.Fatal("bound/invalid input did not reject without a candidate")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := p.Prepare(ctx, input, nil); out != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled preparation accepted")
	}
	if _, err := configuration.NewPreparer(configuration.Limits{}); err == nil {
		t.Fatal("missing bounds accepted")
	}
}

func TestSingleUserShadowsocks2022BecomesAnExplicitAESUserCollection(t *testing.T) {
	for _, method := range []string{"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm"} {
		config := configurationFixture(&ss2022.ServerConfig{Method: method, Key: "server-key", Email: "old@local", Level: 7})
		p, err := configuration.NewPreparer(configuration.Limits{MaxBytes: 4096, MaxUsers: 1})
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := p.Prepare(t.Context(), marshal(t, config), []domain.InboundUserSet{{InboundTag: "selected"}})
		if err != nil {
			t.Fatal(err)
		}
		var compiled core.Config
		if err := proto.Unmarshal(candidate, &compiled); err != nil {
			t.Fatal(err)
		}
		settings := compiled.Inbound[0].ProxySettings
		if settings.Type != serial.GetMessageType(&ss2022.MultiUserServerConfig{}) {
			t.Fatal("empty desired collection retained a single-user server")
		}
		var actual ss2022.MultiUserServerConfig
		if err := proto.Unmarshal(settings.Value, &actual); err != nil {
			t.Fatal(err)
		}
		if actual.Method != method || actual.Key != "server-key" || len(actual.Users) != 0 {
			t.Fatal("single-to-multi conversion lost server settings or retained a user")
		}
	}
	for _, single := range []*ss2022.ServerConfig{{Method: "2022-blake3-chacha20-poly1305"}, {Method: "2022-blake3-aes-128-gcm"}} {
		if single.Method != "2022-blake3-chacha20-poly1305" {
			single.ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 0x01})
		}
		p, _ := configuration.NewPreparer(configuration.Limits{MaxBytes: 4096, MaxUsers: 1})
		candidate, err := p.Prepare(t.Context(), marshal(t, configurationFixture(single)), []domain.InboundUserSet{{InboundTag: "selected"}})
		if candidate != nil || !errors.Is(err, domain.ErrUserManagementUnsupported) {
			t.Fatal("unsupported single-user conversion did not reject safely")
		}
	}
}

func TestMalformedOrAmbiguousSelectedNativeSettingsAreRejected(t *testing.T) {
	for _, corrupt := range []func(*core.Config){
		func(c *core.Config) { c.Inbound[0].ProxySettings.Value = []byte{0xff} },
		func(c *core.Config) { c.Inbound[0].ProxySettings = nil },
		func(c *core.Config) { c.Inbound[1].Tag = c.Inbound[0].Tag },
	} {
		config := configurationFixture(&vless.Config{Decryption: "none"})
		corrupt(config)
		p, _ := configuration.NewPreparer(configuration.Limits{MaxBytes: 4096, MaxUsers: 1})
		candidate, err := p.Prepare(t.Context(), marshal(t, config), []domain.InboundUserSet{{InboundTag: "selected"}})
		if candidate != nil || !errors.Is(err, domain.ErrInvalidUserConfiguration) {
			t.Fatal("malformed or ambiguous selection returned a candidate")
		}
	}
}

func configurationFixture(settings proto.Message) *core.Config {
	config := &core.Config{Inbound: []*core.InboundHandlerConfig{
		{Tag: "selected", ProxySettings: serial.ToTypedMessage(settings), ReceiverSettings: &serial.TypedMessage{Type: "untouched-receiver", Value: []byte{1, 2}}},
		{Tag: "untouched", ProxySettings: &serial.TypedMessage{Type: "future-protocol", Value: []byte{0xff}}},
	}, App: []*serial.TypedMessage{{Type: "future-application", Value: []byte{9}}}}
	config.ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 0x01})
	config.Inbound[0].ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 0x02})
	config.Inbound[0].ProxySettings.ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 0x04})
	return config
}

func marshal(t testing.TB, message proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatal("fixture marshal failed")
	}
	return data
}
