//go:build integration && linux

package integration_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/sync/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/app/stats/command"
)

func TestSyncFromNormalStartup(t *testing.T) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("XRAY_TEST_BINARY must select the pinned executable")
	}
	pki := testutil.NewPKI(t)
	broker := "tls://" + testutil.NewNATSBroker(t, pki.Server, true).Addr().String()
	cfg, err := settings.Load("../../../../../../deploy/config.example.yml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Communications.HTTP.Address, cfg.Communications.HTTP.TLS = "127.0.0.1:0", pki.Server
	cfg.Communications.GRPC.Address, cfg.Communications.GRPC.TLS = "127.0.0.1:0", pki.Server
	cfg.Communications.NATS.Servers, cfg.Communications.NATS.TLS = []string{broker}, pki.Client
	cfg.Communications.Requests.NATS.SubjectPrefix = "fixture.sync"
	native, inbound := listen(t), listen(t)
	nativeAddress := native.Addr().String()
	content := fmt.Appendf(nil, `{"log":{"loglevel":"none"},"api":{"tag":"api","listen":%q,"services":["StatsService","HandlerService"]},"stats":{},"inbounds":[{"tag":"selected","listen":"127.0.0.1","port":%d,"protocol":"vless","settings":{"decryption":"none","clients":[]}}],"outbounds":[{"protocol":"blackhole"}]}`, nativeAddress, inbound.Addr().(*net.TCPAddr).Port)
	source := filepath.Join(t.TempDir(), "xray.json")
	if err := os.WriteFile(source, content, 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Instances = []settings.Instance{{ID: "one", Engine: "xray", StopTimeout: time.Second, Xray: &settings.Xray{
		Binary: binary, Configuration: source, StateDirectory: testutil.NewPrivateDirectory(t), WorkDirectory: testutil.NewPrivateDirectory(t),
		API: &settings.XrayAPI{Network: "tcp", Address: nativeAddress},
	}}}
	log := &addresses{Handler: slog.NewTextHandler(io.Discard, nil), values: make(map[string]string)}
	app := bootstrap.New(cfg, slog.New(log))
	ctx, cancel := context.WithTimeout(context.Background(), cfg.StartupTimeout)
	defer cancel()
	_ = native.Close()
	_ = inbound.Close()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})

	conn, err := rpc.New(rpc.Config{Network: "tcp", Address: nativeAddress})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := command.NewStatsServiceClient(conn).GetSysStats(ctx, &command.SysStatsRequest{}); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native API did not become ready")
		case <-ticker.C:
		}
	}

	for index, kind := range []string{"http", "grpc", "nats"} {
		t.Run(kind, func(t *testing.T) {
			operator := connect(t, kind, pki, broker, log)
			revision := operator.revision(t)
			if revision.Config != 0 || revision.Generation != 1 || revision.Control != uint64(index) {
				t.Fatalf("unexpected revision: %v", revision)
			}
			membership := true
			request := &v1.SyncRequest{
				InstanceId: "one", OperationId: "startup-" + kind, Expected: revision, Mode: v1.Mode_DELTA,
				Selection: []string{"selected"}, Sets: []*v1.UserSet{{InboundTag: "selected", Accounts: [][]byte{nativeUser(index)}, MembershipPresent: &membership}},
			}
			preview := &v1.Preview{}
			if kind == "grpc" {
				var err error
				preview, err = operator.grpc.PreviewSync(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if kind == "http" {
					request.InstanceId = ""
				}
				operator.json(t, "POST", "/v1/xray/users/instances/one/sync/plans", "xray.users.sync.v1.preview", request, preview)
			}
			if len(preview.Differences) != 1 || preview.Differences[0].Added != 1 {
				t.Fatalf("unexpected preview: %v", preview)
			}
			apply := &v1.ApplyRequest{InstanceId: "one", PlanId: preview.PlanId, Expected: preview.Revision}
			result := &v1.Result{}
			if kind == "grpc" {
				var err error
				result, err = operator.grpc.ApplySync(ctx, apply)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if kind == "http" {
					apply.InstanceId = ""
				}
				operator.json(t, "POST", "/v1/xray/users/instances/one/sync/applications", "xray.users.sync.v1.apply", apply, result)
			}
			if result.Effect != "completed" || len(result.Steps) != 1 || !result.Steps[0].Acknowledged {
				t.Fatalf("unexpected application: %v", result)
			}
		})
	}
}
