//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/configuration"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/protobuf/proto"
)

func TestNativeSourceSelectionSupportsEveryAccountFamily(t *testing.T) {
	compiler := accountCompiler(t)
	p := configuration.Preparer{Compiler: compiler}
	for _, test := range nativeAccountFamilies() {
		t.Run(test.name, func(t *testing.T) {
			source := familySource(t, test, "selected@example.test", 2)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			raw, err := p.Prepare(ctx, "a", "members", "selected@example.test", []configs.Source{{Name: "account.json", Content: source}})
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

func TestNativeSourceBatchPreservesFamilyCredentialFields(t *testing.T) {
	compiler := accountCompiler(t)
	p := configuration.Preparer{Compiler: compiler}
	families := nativeAccountFamilies()
	var inbounds []any
	var selections []domain.SourceSelection
	for _, family := range families {
		inbound := family.inbound("Exact@example.test", 3)
		inbound["tag"] = family.name
		settings := inbound["settings"].(map[string]any)
		if family.name == "vless" {
			settings["clients"].([]any)[0].(map[string]any)["flow"] = "xtls-rprx-vision"
		}
		if family.name == "wireguard" {
			peer := settings["peers"].([]any)[0].(map[string]any)
			peer["preSharedKey"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
			peer["keepAlive"] = 17
			peer["allowedIPs"] = []string{"10.55.0.3/32", "fd00::3/128"}
		}
		inbounds = append(inbounds, inbound)
		selections = append(selections, domain.SourceSelection{InboundTag: family.name, Email: "Exact@example.test"})
	}
	content, _ := json.Marshal(map[string]any{"inbounds": inbounds})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := p.PrepareMany(ctx, "a", selections, []configs.Source{{Name: "families.json", Content: content}})
	if err != nil || len(result) != len(families) {
		t.Fatal(result, err)
	}
	for i, family := range families {
		var user protocol.User
		if result[i].Err != nil || proto.Unmarshal(result[i].Configuration, &user) != nil || user.Email != "Exact@example.test" || user.Level != 7 || user.Account == nil || user.Account.Type != family.account {
			t.Fatal(family.name, result[i].Err, &user)
		}
		switch family.name {
		case "vless":
			var account vless.Account
			if proto.Unmarshal(user.Account.Value, &account) != nil || account.Flow != "xtls-rprx-vision" {
				t.Fatal("configured VLESS flow lost", &account)
			}
		case "shadowsocks2022":
			key := nativeStringField(t, user.Account.Value, 1)
			if len(key) != 1 || key[0] != base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 16)) {
				t.Fatal("SS2022 key lost")
			}
		case "wireguard":
			psk, keepAlive, allowedIPs := nativeStringField(t, user.Account.Value, 2), nativeStringField(t, user.Account.Value, 4), nativeStringField(t, user.Account.Value, 5)
			if len(psk) != 1 || psk[0] != hex.EncodeToString(bytes.Repeat([]byte{8}, 32)) || len(keepAlive) != 1 || keepAlive[0] != "17" || len(allowedIPs) != 2 || allowedIPs[1] != "fd00::3/128" {
				t.Fatal("WireGuard peer fields lost")
			}
		}
	}
}
