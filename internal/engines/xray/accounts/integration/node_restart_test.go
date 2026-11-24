package integration_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/bootstrap"
	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const recoveryNodeConfig = "NODE_TEST_ACCOUNT_RECOVERY_CONFIG"

func TestRecoveryNodeProcess(t *testing.T) {
	path := os.Getenv(recoveryNodeConfig)
	if path == "" {
		t.Skip("subprocess entrypoint")
	}
	config, err := settings.Load(path)
	require(t, err)
	ctx, stop := signal.NotifyContext(t.Context(), syscall.SIGTERM)
	defer stop()
	require(t, bootstrap.Run(ctx, config, slog.New(slog.NewTextHandler(io.Discard, nil))))
}

func TestConfirmedAccountsSurviveNodeProcessLoss(t *testing.T) {
	xray := os.Getenv("NODE_TEST_XRAY")
	if xray == "" {
		t.Skip("set NODE_TEST_XRAY to the absolute path of Xray v26.11.9")
	}
	// The Xray management socket has a short Linux pathname limit.
	dir, err := os.MkdirTemp("", "node-recovery-")
	require(t, err)
	t.Cleanup(func() { require(t, os.RemoveAll(dir)) })
	require(t, os.Mkdir(filepath.Join(dir, "state"), 0700))

	httpPort, grpcPort := freePorts(t)
	inbound, err := net.Listen("tcp4", "127.0.0.1:0")
	require(t, err)
	proxyAddress := inbound.Addr().String()
	source := fmt.Sprintf(`{"log":{"loglevel":"none"},"inbounds":[{"tag":"vless","listen":"127.0.0.1","port":%d,"protocol":"vless","settings":{"clients":[{"email":"baseline","id":%q}],"decryption":"none"}}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`, inbound.Addr().(*net.TCPAddr).Port, credential)
	require(t, os.WriteFile(filepath.Join(dir, "xray.json"), []byte(source), 0600))
	settingsPath := filepath.Join(dir, "node.yml")
	config := fmt.Sprintf(`management:
  http_address: "127.0.0.1:%d"
  grpc_address: "127.0.0.1:%d"
xray:
  id: primary
  executable: %q
  config_file: xray.json
  runtime_directory: .
  state_directory: state
`, httpPort, grpcPort, xray)
	require(t, os.WriteFile(settingsPath, []byte(config), 0600))
	require(t, inbound.Close())

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	origin := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	management := &http.Client{Timeout: 2 * time.Second}
	t.Cleanup(management.CloseIdleConnections)
	connection, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", grpcPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require(t, err)
	t.Cleanup(func() { require(t, connection.Close()) })
	client := accountsv1.NewXrayAccountsServiceClient(connection)
	start := func() *exec.Cmd {
		t.Helper()
		process := startRecoveryNode(t, settingsPath)
		awaitRecoveryNode(t, ctx, management, origin)
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, origin+executionPath, nil)
		require(t, err)
		response, err := management.Do(request)
		require(t, err)
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("Start returned %s: %s", response.Status, body)
		}
		return process
	}

	first := start()
	remove := &accountsv1.SyncUserRequest{Id: instanceID, InboundTag: "vless", Email: "baseline"}
	add := &accountsv1.SyncUserRequest{Id: instanceID, InboundTag: "vless", Email: "retained", Uuid: new(rotatedCredential)}
	result, err := client.SyncUser(ctx, remove)
	require(t, err)
	expectSync(t, result, 0, 1)
	result, err = client.SyncUser(ctx, add)
	require(t, err)
	expectSync(t, result, 1, 0)

	// An existing temporary file makes the next publication fail before rename.
	require(t, os.WriteFile(filepath.Join(dir, "state", "accounts.pending"), []byte("interrupted write"), 0600))
	volatile := &accountsv1.SyncUserRequest{Id: instanceID, InboundTag: "vless", Email: "volatile", Uuid: new(credential)}
	result, err = client.SyncUser(ctx, volatile)
	require(t, err)
	if result.GetOutcome() != "partial" || result.GetAdded() != 1 || result.GetFailure() != "recovery_write_failed" {
		t.Fatalf("sync with failed publication = %v", result)
	}

	require(t, first.Process.Kill())
	if err := first.Wait(); err == nil {
		t.Fatal("first Node process did not report abrupt termination")
	}
	for {
		proxy, err := net.DialTimeout("tcp4", proxyAddress, 100*time.Millisecond)
		if err != nil {
			break
		}
		proxy.Close()
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("old Xray child still owns the inbound port")
		}
	}

	start()
	for _, request := range []*accountsv1.SyncUserRequest{remove, add,
		{Id: instanceID, InboundTag: "vless", Email: volatile.Email}} {
		result, err := client.SyncUser(ctx, request, grpc.WaitForReady(true))
		require(t, err)
		expectSync(t, result, 0, 0)
	}
}

func startRecoveryNode(t *testing.T, settingsPath string) *exec.Cmd {
	t.Helper()
	binary, err := os.Executable()
	require(t, err)
	command := exec.Command(binary, "-test.run=^TestRecoveryNodeProcess$")
	command.Env = append(os.Environ(), recoveryNodeConfig+"="+settingsPath)
	logFile, err := os.CreateTemp(filepath.Dir(settingsPath), "node-log-")
	require(t, err)
	command.Stdout, command.Stderr = logFile, logFile
	require(t, command.Start())
	require(t, logFile.Close())
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
		if t.Failed() {
			body, _ := os.ReadFile(logFile.Name())
			t.Logf("Node subprocess: %s", body)
		}
	})
	return command
}

func awaitRecoveryNode(t *testing.T, ctx context.Context, client *http.Client, origin string) {
	t.Helper()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/healthz", nil)
		require(t, err)
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				return
			}
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("Node subprocess never became ready")
		}
	}
}
