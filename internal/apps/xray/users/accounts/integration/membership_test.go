//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/configuration"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestPreparedMembershipRunsAndClearsEveryNativeAccountFamily(t *testing.T) {
	compiler := accountCompiler(t)
	pki := testutil.NewPKI(t)
	for _, family := range nativeAccountFamilies() {
		t.Run(family.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			target := accountTarget(t)
			f := newFixture(t, pki, "", "http")
			configureFamily(t, &f, family)
			address, virtualTarget := familyTrafficTarget(t, f, target.Listener.Addr().String())
			proxy, clientAPI := familyTrafficClient(t, &f, family, "client", address, "prepared@example.test", 3)
			input, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
			if err != nil {
				t.Fatal(err)
			}
			if family.name == "shadowsocks2022" {
				// Exercise the native empty-JSON -> single-user config conversion
				// as well as clearing the prepared multi-user config below.
				var config map[string]any
				if err := json.Unmarshal(input, &config); err != nil {
					t.Fatal(err)
				}
				config["inbounds"].([]any)[0].(map[string]any)["settings"].(map[string]any)["clients"] = []any{}
				input, err = json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
			}
			compiled, err := compiler.Compile(ctx, "a", []configs.Source{{Name: "config.json", Content: input}})
			if err != nil {
				t.Fatal(err)
			}
			preparer := configuration.Preparer{Compiler: compiler, Factories: map[string]configuration.Factory{"a": api.New(nil)}}
			account, err := preparer.Prepare(ctx, "a", "members", "prepared@example.test", []configs.Source{{Name: "accounts.json", Content: familySource(t, family, "prepared@example.test", 3)}})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := configuration.ReplaceUsers(ctx, compiled.Content, []domain.InboundUserSet{{InboundTag: "members", Accounts: [][]byte{account.Configuration}}})
			if err != nil {
				t.Fatal(err)
			}
			// Start the actual candidate protobuf, not its JSON source or a fake SDK.
			path := filepath.Join(t.TempDir(), "engine.pb")
			if err := os.WriteFile(path, prepared, 0600); err != nil {
				t.Fatal(err)
			}
			f.config.Instances[0].Xray.Configuration = path
			c := startNode(t, f)
			awaitNative(t, f.native)
			awaitNative(t, clientAPI)
			assertFamilyCount(t, c, "http", 1)
			if familyMember(t, c, "http", "original@example.test") != nil || familyMember(t, c, "http", "prepared@example.test") == nil {
				t.Fatal("prepared replacement did not replace native membership")
			}
			if err := familyTraffic(t, proxy, virtualTarget); err != nil {
				t.Fatal("prepared account failed real authentication", err)
			}
			cleared, err := configuration.ReplaceUsers(ctx, prepared, []domain.InboundUserSet{{InboundTag: "members"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, cleared, 0600); err != nil {
				t.Fatal(err)
			}
			restartAccountInstance(t, c)
			assertFamilyCount(t, c, "http", 0)
			if result, code := accountMutation(t, c, "http", "add", "", account.Configuration); code != "" || !result.Acknowledged {
				t.Fatal("empty prepared config lost native account management", result, code)
			}
			assertFamilyCount(t, c, "http", 1)
		})
	}
}
