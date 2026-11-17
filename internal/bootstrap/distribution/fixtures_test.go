//go:build distribution && integration && linux

package distribution_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
)

func foundationConfig(pki testutil.PKI, brokerURL string) settings.Config {
	return settings.Config{
		StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second,
		Communications: settings.Communications{HTTP: &settings.HTTP{
			Address: "127.0.0.1:0", TLS: pki.Server,
			ReadTimeout: time.Second, WriteTimeout: time.Second,
			IdleTimeout: time.Second, ShutdownTimeout: time.Second,
			BodyLimit: 1024, Concurrency: 16,
		}, GRPC: &settings.GRPC{
			Address: "127.0.0.1:0", TLS: pki.Server, ConnectionTimeout: 200 * time.Millisecond, ShutdownTimeout: time.Second,
			MaxRecvMessageSize: 1024, MaxSendMessageSize: 1024, MaxConnections: 16, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192,
		}, NATS: &settings.NATS{
			Servers: []string{strings.Replace(brokerURL, "nats://", "tls://", 1)}, TLS: pki.Client, TLSHandshakeFirst: true,
			ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second,
			ReconnectWait: time.Second, MaxReconnects: 2, ReconnectBufferSize: 1024, PingInterval: time.Second, MaxPingsOutstanding: 2,
		}},
	}
}

func distributionExecute(directory string, program string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, program, args...)
	command.Dir = directory
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = time.Second
	return command.CombinedOutput()
}

func distributionCommand(t *testing.T, directory string, program string, args ...string) string {
	t.Helper()
	output, err := distributionExecute(directory, program, args...)
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", program, err, output)
	}
	return string(output)
}

func readDistributionFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func copyDistributionIdentity(t *testing.T, directory, name string, files mtls.Files) mtls.Files {
	t.Helper()
	for suffix, source := range map[string]string{".crt": files.Certificate, ".key": files.PrivateKey, "-ca.crt": files.CA} {
		if err := os.WriteFile(filepath.Join(directory, name+suffix), readDistributionFile(t, source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return mtls.Files{Certificate: name + ".crt", PrivateKey: name + ".key", CA: name + "-ca.crt"}
}

type distributionFile struct {
	path string
	hash [32]byte
	mode os.FileMode
}

func distributionFiles(t *testing.T, directory string) []distributionFile {
	t.Helper()
	var files []distributionFile
	if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, distributionFile{path, sha256.Sum256(data), info.Mode()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func assertDistributionFiles(t *testing.T, files []distributionFile) {
	t.Helper()
	for _, file := range files {
		info, err := os.Stat(file.path)
		if err != nil {
			t.Fatal(err)
		}
		if sha256.Sum256(readDistributionFile(t, file.path)) != file.hash || info.Mode() != file.mode {
			t.Fatalf("installation changed private file: %s", file.path)
		}
	}
}

type transportAddresses struct{ HTTP, GRPC string }

func distributionReadiness(t *testing.T, name string) transportAddresses {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "docker", "logs", "--follow", name)
	reader, err := command.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	ready := make(chan transportAddresses, 1)
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(reader)
		var addresses transportAddresses
		connected, sent := false, false
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
				addresses.HTTP = event.Address
			case "gRPC listening":
				addresses.GRPC = event.Address
			case "NATS connected":
				connected = true
			}
			if addresses.HTTP != "" && addresses.GRPC != "" && connected && !sent {
				ready <- addresses
				sent = true
			}
		}
		_ = command.Wait()
	}()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case addresses := <-ready:
		return addresses
	case <-done:
		t.Fatal("packaged Node exited before all transports started")
	case <-time.After(8 * time.Second):
		t.Fatal("packaged Node did not start all transports")
	}
	return transportAddresses{}
}
