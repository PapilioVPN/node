//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/testutil"
	native "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

func TestSyncMembershipPortUsesNormalCompositionAndReviewedReplay(t *testing.T) {
	pki := testutil.NewPKI(t)
	f, address := plannedFixture(t, pki, "", "http")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.resources.listeners = append(f.resources.listeners, listener)
	var root map[string]any
	if err := json.Unmarshal(f.original, &root); err != nil {
		t.Fatal(err)
	}
	root["inbounds"] = append(root["inbounds"].([]any), map[string]any{"tag": "members", "listen": "127.0.0.1", "port": listener.Addr().(*net.TCPAddr).Port, "protocol": "vless", "settings": map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": "11111111-1111-4111-8111-111111111111", "email": "old@example.test"}}}})
	f.original, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.directory, "active.json"), f.original, 0600); err != nil {
		t.Fatal(err)
	}
	var members *services.Memberships
	c := startNode(t, f, fx.Invoke(func(value *services.Memberships) { members = value }))
	awaitTraffic(t, f)
	sources := []domain.Source{{Name: "base.json", Content: f.original}}
	exerciseStateReplacement(t, c, "http", sources, 0, "1", "2")
	awaitTraffic(t, f)
	_, generation := execution(t, c)
	account, err := proto.Marshal(&protocol.User{Email: "new@example.test", Account: serial.ToTypedMessage(&vless.Account{Id: "22222222-2222-4222-8222-222222222222"})})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	plan, err := members.Plan(ctx, "edge-a", 1, generation, []accounts.InboundUserSet{{InboundTag: "members", Accounts: [][]byte{account}}})
	if err != nil || plan.Mode != domain.Restart {
		t.Fatal(plan, err)
	}
	result, err := members.Apply(ctx, "edge-a", plan.ID)
	if err != nil || result.Readiness != "api_ready" || result.RequiresRecovery || !result.ReceiptDurable {
		t.Fatal(result, err)
	}
	awaitTraffic(t, f)
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	users, err := native.NewHandlerServiceClient(connection).GetInboundUsers(ctx, &native.GetInboundUserRequest{Tag: "members"})
	if err != nil || len(users.Users) != 1 || users.Users[0].Email != "new@example.test" {
		t.Fatal(users, err)
	}
	oldID, oldGeneration := execution(t, c)
	replay, err := members.Apply(ctx, "edge-a", plan.ID)
	after, newGeneration := execution(t, c)
	if err != nil || replay.Revision != 2 || oldID != after || oldGeneration != newGeneration {
		t.Fatal(replay, err)
	}
}
