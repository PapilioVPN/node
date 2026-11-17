//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	ss2022 "github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"google.golang.org/protobuf/proto"

	xrayprocess "github.com/PapilioVPN/node/internal/apps/core/engines/instances/infra/xray/process"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/configuration"
	"github.com/PapilioVPN/node/internal/testutil"
)

// Execute the candidate in standalone Xray, independently of live user mutations.
// Membership is verified for eight families; authentication here covers VLESS.
func TestPreparedUserCollectionsExecuteReplaceClearAndReplay(t *testing.T) {
	fixture := nativeConfiguration(t)
	preparer, err := configuration.NewPreparer(configuration.Limits{MaxBytes: 1 << 20, MaxUsers: 8})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.Prepare(t.Context(), fixture.compiled, fixture.users)
	if err != nil {
		t.Fatal(err)
	}
	clear := make([]domain.InboundUserSet, len(fixture.users))
	for i, set := range fixture.users {
		clear[i].InboundTag = set.InboundTag
	}
	cleared, err := preparer.Prepare(t.Context(), prepared, clear)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(fixture.instance.Xray.ConfigFile), "candidate.pb")
	runtime, err := xrayprocess.New(xrayprocess.Config{Binary: fixture.instance.Xray.Binary, ConfigFile: path, StopTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
		fixture.removeStoppedAPISocket(t)
		fixture.assertReleased(t)
	})
	fixture.startClient(t)
	for _, phase := range []struct {
		name               string
		content            []byte
		email              string
		level              uint32
		accepted, rejected string
	}{
		{"original", fixture.compiled, "original@local", 3, fixture.oldProxy, fixture.newProxy},
		{"replace", prepared, "desired@local", 7, fixture.newProxy, fixture.oldProxy},
		{"clear", cleared, "", 0, "", fixture.newProxy},
		{"replay", prepared, "desired@local", 7, fixture.newProxy, fixture.oldProxy},
	} {
		t.Run(phase.name, func(t *testing.T) {
			if err := os.WriteFile(path, phase.content, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := runtime.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			if err := fixture.traffic(ctx, fixture.health, fixture.untouchedProxy); err != nil {
				t.Fatalf("prepared engine or untouched inbound unavailable: %v", err)
			}
			client := testutil.XrayAPIClient(t, fixture.instance.Xray.API)
			handler, err := client.HandlerClient()
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range fixture.users {
				result, err := handler.GetInboundUsers(ctx, &native.GetInboundUserRequest{Tag: set.InboundTag})
				if err != nil || result == nil {
					t.Fatalf("%s native membership unavailable: %v", set.InboundTag, err)
				}
				want := 1
				if phase.email == "" {
					want = 0
				}
				if len(result.Users) != want {
					t.Fatalf("%s native membership count = %d; want %d", set.InboundTag, len(result.Users), want)
				}
				if want == 1 && (result.Users[0].Email != phase.email || result.Users[0].Level != phase.level) {
					t.Fatalf("%s native identity/level changed", set.InboundTag)
				}
			}
			if phase.accepted != "" {
				if err := fixture.traffic(ctx, phase.accepted); err != nil {
					t.Fatal("configured VLESS credential did not authenticate")
				}
			}
			blocked, release := context.WithTimeout(ctx, 300*time.Millisecond)
			err = fixture.traffic(blocked, phase.rejected)
			release()
			if err == nil {
				t.Fatal("absent VLESS credential authenticated a new connection")
			}
			if err := runtime.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			fixture.removeStoppedAPISocket(t)
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
	original, err := os.ReadFile(fixture.instance.Xray.ConfigFile)
	if err != nil || !bytes.Equal(original, fixture.original) {
		t.Fatal("operator configuration changed")
	}
}

// Native JSON building chooses single-user mode for zero SS2022 clients. Both
// populated and empty projections must execute as explicit multi-user servers.
func TestEmptyShadowsocks2022ConfigurationSupportsPreparedCollections(t *testing.T) {
	fixture := nativeConfiguration(t)
	var source map[string]any
	if err := json.Unmarshal(fixture.original, &source); err != nil {
		t.Fatal(err)
	}
	for _, value := range source["inbounds"].([]any) {
		inbound := value.(map[string]any)
		if inbound["tag"] == "ss2022" {
			inbound["settings"].(map[string]any)["clients"] = []any{}
		}
	}
	writeJSON(t, fixture.instance.Xray.ConfigFile, source)
	compiled := compileConfiguration(t, fixture.instance)
	var original core.Config
	if err := proto.Unmarshal(compiled, &original); err != nil {
		t.Fatal(err)
	}
	foundSingle := false
	for _, inbound := range original.Inbound {
		if inbound.Tag == "ss2022" {
			foundSingle = inbound.ProxySettings.Type == serial.GetMessageType(&ss2022.ServerConfig{})
		}
	}
	if !foundSingle {
		t.Fatal("native empty-client build no longer produces a single-user server")
	}
	preparer, err := configuration.NewPreparer(configuration.Limits{MaxBytes: 1 << 20, MaxUsers: 1})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(fixture.instance.Xray.ConfigFile), "candidate.pb")
	runtime, err := xrayprocess.New(xrayprocess.Config{Binary: fixture.instance.Xray.Binary, ConfigFile: path, StopTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
		fixture.removeStoppedAPISocket(t)
		fixture.assertReleased(t)
	})
	fixture.startClient(t)
	var desired []domain.InboundUser
	for _, set := range fixture.users {
		if set.InboundTag == "ss2022" {
			desired = set.Users
		}
	}
	if len(desired) != 1 {
		t.Fatal("missing desired SS2022 fixture user")
	}
	for _, users := range [][]domain.InboundUser{desired, nil} {
		// Project each case from the original single-user message, not a previously
		// prepared multi-user candidate; even an initial clear must change its type.
		candidate, err := preparer.Prepare(t.Context(), compiled, []domain.InboundUserSet{{InboundTag: "ss2022", Users: users}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, candidate, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runtime.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
		if err := fixture.traffic(ctx, fixture.health); err != nil {
			cancel()
			t.Fatal(err)
		}
		client := testutil.XrayAPIClient(t, fixture.instance.Xray.API)
		handler, err := client.HandlerClient()
		if err != nil {
			t.Fatal(err)
		}
		result, err := handler.GetInboundUsers(ctx, &native.GetInboundUserRequest{Tag: "ss2022"})
		cancel()
		if err != nil || result == nil || len(result.Users) != len(users) {
			t.Fatalf("single-to-multi projection did not install exactly the selected users: %v", err)
		}
		if err := runtime.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		fixture.removeStoppedAPISocket(t)
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
