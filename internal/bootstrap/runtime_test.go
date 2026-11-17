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

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"go.yaml.in/yaml/v3"
)

func TestExecutableServesMutualTLSAndStopsOnSIGTERM(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "node")
	buildContext, cancelBuild := context.WithTimeout(context.Background(), time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildContext, "go", "build", "-p=2", "-o", binary, "github.com/PapilioVPN/node/cmd/node")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build runtime: %v\n%s", err, output)
	}
	pki := testutil.NewPKI(t)
	config := settings.Config{
		StartupTimeout: 3 * time.Second, ShutdownTimeout: 3 * time.Second,
		Communications: settings.Communications{HTTP: &settings.HTTP{
			Address: "127.0.0.1:0", TLS: pki.Server,
			ReadTimeout: time.Second, WriteTimeout: time.Second,
			IdleTimeout: time.Second, ShutdownTimeout: time.Second,
			BodyLimit: 1024, Concurrency: 16,
		}},
	}
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
	go func() {
		defer close(logsDone)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			var event struct {
				Message string `json:"msg"`
				Address string `json:"address"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Message == "HTTP listening" {
				select {
				case address <- event.Address:
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
}
