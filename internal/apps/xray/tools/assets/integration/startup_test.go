//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	instancev1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/installation"
	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func TestAssetsFromNormalStartup(t *testing.T) {
	f, native := prepare(t)
	pki := testutil.NewPKI(t)
	broker := assetBroker(t, pki)
	distribution := t.TempDir()
	base := candidate(t, "PROOF", "original.test")
	for name, file := range map[string]domain.File{"xray": base.Binary, "geoip.dat": base.GeoIP, "geosite.dat": base.GeoSite} {
		if err := os.WriteFile(filepath.Join(distribution, name), file.Content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := installation.Initialize(ctx, "one", f.root, distribution); err != nil {
		t.Fatal(err)
	}
	cfg, err := settings.Load("../../../../../../deploy/config.example.yml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Communications.Requests.Timeout = 20 * time.Second
	cfg.Communications.HTTP.Address, cfg.Communications.HTTP.TLS = address(t), pki.Server
	cfg.Communications.HTTP.ReadTimeout, cfg.Communications.HTTP.WriteTimeout = 20*time.Second, 25*time.Second
	cfg.Communications.HTTP.BodyLimit = 64 << 20
	cfg.Communications.GRPC.Address, cfg.Communications.GRPC.TLS = address(t), pki.Server
	cfg.Communications.NATS.Servers, cfg.Communications.NATS.TLS = []string{broker}, pki.Client
	cfg.Communications.Requests.NATS.SubjectPrefix = "fixture.assets"
	cfg.Instances = []settings.Instance{{ID: "one", Engine: "xray", StopTimeout: time.Second, Xray: &settings.Xray{
		Binary: filepath.Join(f.root, "current", "xray"), Configuration: f.config, AssetsDirectory: f.root,
		API: &settings.XrayAPI{Network: "tcp", Address: native},
	}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	logs := &eventLog{}
	app := bootstrap.New(cfg, slog.New(slog.NewJSONHandler(logs, nil)))
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	tls, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	httpTransport := &http.Transport{TLSClientConfig: tls}
	t.Cleanup(httpTransport.CloseIdleConnections)
	client := &controller{
		transport: "http", http: &http.Client{Transport: httpTransport, Timeout: 25 * time.Second}, url: "https://" + cfg.Communications.HTTP.Address,
	}
	conn, err := grpc.NewClient(cfg.Communications.GRPC.Address, grpc.WithTransportCredentials(credentials.NewTLS(tls)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	instances := instancev1.NewInstancesServiceClient(conn)
	nc, err := nats.Connect(broker, nats.Secure(tls), nats.TLSHandshakeFirst(), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	old := client.inspect(t)
	var next domain.Release
	if data := client.call(t, "stage", map[string]any{"candidate": candidate(t, "PROOF", "updated.test")}); json.Unmarshal(data, &next) != nil || next.ID == "" {
		t.Fatalf("stage failed: %s", data)
	}
	input := &instancev1.GetInstanceStatusRequest{InstanceId: "one"}
	before, err := instances.GetInstanceStatus(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	client.transport, client.grpc = "grpc", conn
	activation := client.call(t, "activate", map[string]any{
		"release_id": next.ID, "revision": old.State.Revision, "execution_id": before.GetExecutionId(),
	})
	var activated domain.Receipt
	if err := json.Unmarshal(activation, &activated); err != nil || !activated.Ready || activated.State.Active != next.ID {
		t.Fatalf("activate: %s (%v)", activation, err)
	}
	after, err := instances.GetInstanceStatus(ctx, input)
	if err != nil || after.GetExecutionId() == before.GetExecutionId() {
		t.Fatal("activation did not replace the actual execution", err)
	}
	f.traffic(t)
	client.transport, client.nats = "nats", nc
	rollback := client.call(t, "rollback", map[string]any{
		"expected_revision": strconv.FormatUint(activated.State.Revision, 10), "execution_id": after.GetExecutionId(),
	})
	var restored domain.Receipt
	if err := json.Unmarshal(rollback, &restored); err != nil || !restored.Ready || restored.State.Active != old.State.Active {
		t.Fatalf("rollback: %s (%v)", rollback, err)
	}
	f.traffic(t)
}
