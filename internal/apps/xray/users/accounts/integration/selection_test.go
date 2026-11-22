//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	configservices "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/configuration"
	"github.com/xtls/xray-core/common/protocol"
	"google.golang.org/protobuf/proto"
)

func TestNativeSourceSelectionSupportsEveryAccountFamily(t *testing.T) {
	admission, err := process.NewAdmission(2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := admission.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	builder, err := process.New(process.Config{Binary: os.Getenv("XRAY_TEST_BINARY"), Directory: t.TempDir(), WorkDirectory: t.TempDir()}, admission)
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := configservices.New([]configservices.Binding{{ID: "a", Compiler: builder}})
	if err != nil {
		t.Fatal(err)
	}
	p := configuration.Preparer{Compiler: compiler}
	key := func(value byte, n int) string {
		return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{value}, n))
	}
	member := func(fields map[string]any) map[string]any {
		fields["email"] = "selected@example.test"
		fields["level"] = 7
		return fields
	}
	for _, test := range []struct {
		name, proxy, account string
		settings             map[string]any
	}{
		{"vless", "vless", "xray.proxy.vless.Account", map[string]any{"decryption": "none", "clients": []any{member(map[string]any{"id": managedID})}}},
		{"vmess", "vmess", "xray.proxy.vmess.Account", map[string]any{"clients": []any{member(map[string]any{"id": managedID})}}},
		{"trojan", "trojan", "xray.proxy.trojan.Account", map[string]any{"clients": []any{member(map[string]any{"password": "fixture"})}}},
		{"shadowsocks", "shadowsocks", "xray.proxy.shadowsocks.Account", map[string]any{"clients": []any{member(map[string]any{"method": "aes-128-gcm", "password": "fixture"})}}},
		{"shadowsocks2022", "shadowsocks", "xray.proxy.shadowsocks_2022.Account", map[string]any{"method": "2022-blake3-aes-128-gcm", "password": key(1, 16), "clients": []any{member(map[string]any{"password": key(2, 16)})}}},
		{"wireguard", "wireguard", "xray.proxy.wireguard.PeerConfig", map[string]any{"secretKey": key(1, 32), "address": []string{"10.55.0.1/24"}, "peers": []any{member(map[string]any{"publicKey": key(2, 32), "allowedIPs": []string{"10.55.0.2/32"}})}}},
		{"hysteria", "hysteria", "xray.proxy.hysteria.account.Account", map[string]any{"version": 2, "clients": []any{member(map[string]any{"auth": "fixture"})}}},
		{"masque", "masque", "xray.proxy.masque.Account", map[string]any{"address": []string{"10.56.0.1/24"}, "clients": []any{member(map[string]any{"pass": "fixture"})}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, err := json.Marshal(map[string]any{"inbounds": []any{map[string]any{"tag": "selected", "port": 1, "protocol": test.proxy, "settings": test.settings}}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			raw, err := p.Prepare(ctx, "a", "selected", "selected@example.test", []configs.Source{{Name: "account.json", Content: source}})
			if err != nil {
				t.Fatal(err)
			}
			var user protocol.User
			if err := proto.Unmarshal(raw, &user); err != nil || user.Email != "selected@example.test" || user.Level != 7 || user.Account == nil || user.Account.Type != test.account {
				t.Fatal(&user, err)
			}
		})
	}
}
