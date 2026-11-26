package bootstrap

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	bridgerpc "github.com/PapilioVPN/go-bridge/api/grpc/client"
	instancesv1 "github.com/PapilioVPN/node/internal/engines/hysteria/instances/bridge/grpc/proto/v1"
	logsv1 "github.com/PapilioVPN/node/internal/engines/hysteria/logs/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const hysteriaInstance = "/v1/hysteria/instances/edge"

func TestHysteriaDisabledInNode(t *testing.T) {
	httpAddr, grpcAddr := availableAddresses(t)
	stopNode := runHysteriaNode(t, settings.Config{Management: settings.Management{HTTPAddress: httpAddr, GRPCAddress: grpcAddr}})
	t.Cleanup(func() { stopNode() })
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	checkHTTP(t, ctx, http.DefaultClient, "http://"+httpAddr+hysteriaInstance, "", http.StatusNotFound)
	conn, err := bridgerpc.NewClient(bridgerpc.Config{Target: grpcAddr}, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = instancesv1.NewHysteriaInstancesServiceClient(conn).GetStatus(ctx, &instancesv1.HysteriaInstanceRequest{Id: "edge"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("disabled Hysteria RPC returned %v", err)
	}
}

func TestHysteriaNodeDrainsLogsAndStopsNativeServer(t *testing.T) {
	binary := os.Getenv("NODE_TEST_HYSTERIA")
	if binary == "" {
		t.Skip("NODE_TEST_HYSTERIA must point to the pinned official binary")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	httpAddr, grpcAddr := availableAddresses(t)
	statsAddr, _ := availableAddresses(t)
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpAddr := udp.LocalAddr().String()
	udp.Close()
	files, tls := testTLS(t, false)
	const apiKey = "test-node-api-key"
	source := fmt.Sprintf("listen: %q\ntls: {cert: %q, key: %q}\nauth: {type: password, password: fixture-client}\ntrafficStats: {listen: %q, secret: fixture-stats}\n", udpAddr, files.CertFile, files.KeyFile, statsAddr)
	if err := os.WriteFile(filepath.Join(dir, "server.yml"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(dir, "config.yml")
	contents := fmt.Sprintf(`management:
  http_address: %q
  grpc_address: %q
  tls: {cert_file: %q, key_file: %q}
  api_key: %q
hysteria:
  instances:
    - {id: edge, executable: %q, config_file: server.yml, runtime_directory: .}
`, httpAddr, grpcAddr, files.CertFile, files.KeyFile, apiKey, binary)
	if err := os.WriteFile(settingsPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := settings.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	stopNode := runHysteriaNode(t, config)
	defer stopNode()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tls}}
	defer client.CloseIdleConnections()
	base := "https://" + httpAddr + hysteriaInstance
	checkHTTP(t, ctx, client, base, "", http.StatusUnauthorized)
	checkHTTP(t, ctx, client, base, apiKey, http.StatusOK)
	conn, err := bridgerpc.NewClient(bridgerpc.Config{Target: grpcAddr, TLS: tls, APIKey: apiKey})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	instances := instancesv1.NewHysteriaInstancesServiceClient(conn)
	before, err := instances.GetStatus(ctx, &instancesv1.HysteriaInstanceRequest{Id: "edge"})
	if err != nil || before.GetState() != instancesv1.HysteriaInstanceState_HYSTERIA_INSTANCE_STATE_STOPPED {
		t.Fatalf("initial state %v, %v", before, err)
	}
	started, err := instances.Start(ctx, &instancesv1.StartHysteriaRequest{Id: "edge"})
	if err != nil || started.GetState() != instancesv1.HysteriaInstanceState_HYSTERIA_INSTANCE_STATE_READY ||
		started.GetAppliedConfigSha256() != fmt.Sprintf("%x", sha256.Sum256([]byte(source))) {
		t.Fatalf("start result %v, %v", started, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/logs", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", apiKey)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTP logs returned %s", response.Status)
	}
	stream, err := logsv1.NewHysteriaLogsServiceClient(conn).GetLogs(ctx, &logsv1.HysteriaLogsRequest{Id: "edge"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Header(); err != nil {
		t.Fatal(err)
	}
	stopNode()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("HTTP log drain: %v", err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("gRPC log drain: %v", err)
	}
	listener, err := net.ListenPacket("udp4", udpAddr)
	if err != nil {
		t.Fatalf("native QUIC listener was not released: %v", err)
	}
	listener.Close()
	listenerTCP, err := net.Listen("tcp4", statsAddr)
	if err != nil {
		t.Fatalf("native stats listener was not released: %v", err)
	}
	listenerTCP.Close()
	entries, err := filepath.Glob(filepath.Join(dir, "hysteria-*.yaml"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("candidate survived shutdown: %v, %v", entries, err)
	}
}

func runHysteriaNode(t *testing.T, config settings.Config) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, config, quietLogger()) }()
	return func() {
		if ctx.Err() != nil {
			return
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Node shutdown: %v", err)
			}
		case <-time.After(6 * time.Second):
			t.Error("Node did not drain listeners and stop the engine")
		}
	}
}
