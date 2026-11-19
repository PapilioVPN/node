//go:build integration && linux

package bootstrap_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestExecutableStartsAllTransportsAndStopsOnSIGTERM(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "node")
	// Compilation has its own budget; runtime startup/stop deadlines stay strict.
	buildContext, cancelBuild := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildContext, "go", "build", "-p=2", "-o", binary, "github.com/PapilioVPN/node/cmd/node")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build runtime: %v\n%s", err, output)
	}
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	config := foundationConfig(pki, broker.ClientURL())
	contents, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-config", path)
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done, logsDone := make(chan struct{}), make(chan struct{})
	var exitError error
	go func() { exitError = command.Wait(); close(done) }()
	address := make(chan string, 1)
	grpcAddress := make(chan string, 1)
	natsReady := make(chan struct{}, 1)
	go func() {
		defer close(logsDone)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			var event struct {
				Message string `json:"msg"`
				Address string `json:"address"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) != nil {
				continue
			}
			switch event.Message {
			case "HTTP listening":
				select {
				case address <- event.Address:
				default:
				}
			case "gRPC listening":
				select {
				case grpcAddress <- event.Address:
				default:
				}
			case "NATS connected":
				select {
				case natsReady <- struct{}{}:
				default:
				}
			}
		}
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			_ = command.Process.Kill()
			<-done
		}
		stderr.Close()
		<-logsDone
	})
	var bound string
	select {
	case bound = <-address:
	case <-done:
		t.Fatalf("runtime exited before readiness: %v", exitError)
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not start")
	}
	var grpcBound string
	select {
	case grpcBound = <-grpcAddress:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime gRPC did not start")
	}
	select {
	case <-natsReady:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime NATS did not connect")
	}
	if broker.NumClients() != 1 {
		t.Fatal("runtime has not established its broker connection")
	}

	tlsConfig, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	response, err := client.Get("https://" + bound + "/unregistered")
	if err != nil {
		t.Fatal(err)
	}
	_, copyError := io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if copyError != nil || response.StatusCode != http.StatusNotFound {
		t.Fatal("unexpected foundation response")
	}
	grpcClient, err := grpc.NewClient(grpcBound, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	if err != nil {
		t.Fatal(err)
	}
	defer grpcClient.Close()
	rpcContext, cancelRPC := context.WithTimeout(context.Background(), 2*time.Second)
	_, err = healthv1.NewHealthClient(grpcClient).Check(rpcContext, &healthv1.HealthCheckRequest{})
	cancelRPC()
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("unexpected foundation gRPC response: %v", err)
	}
	grpcClient.Close()

	transport.CloseIdleConnections()
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		if exitError != nil {
			t.Fatalf("runtime shutdown failed: %v", exitError)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not join shutdown")
	}
	// Native client drain and the process exit must release the actual broker socket.
	deadline := time.Now().Add(time.Second)
	for broker.NumClients() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if broker.NumClients() != 0 {
		t.Fatal("runtime left a broker connection open")
	}
}
