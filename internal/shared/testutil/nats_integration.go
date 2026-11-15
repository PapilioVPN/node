//go:build integration

package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/nats-io/nats.go"
)

// NATSBroker owns one real process, private CA identities and restrictive ACLs.
// It never uses a host broker or production credentials.
type NATSBroker struct {
	URL          string
	Certificates Certificates
	cancel       context.CancelFunc
	done         chan struct{}
}

func NewNATSBroker(t testing.TB) *NATSBroker {
	t.Helper()
	binary := os.Getenv("NATS_TEST_BINARY")
	if binary == "" {
		t.Fatal("NATS_TEST_BINARY is required for native broker integration")
	}
	certificates := NewCertificates(t, "node", "controller", "outsider")
	directory := t.TempDir()
	config := fmt.Sprintf(`host: "127.0.0.1"
port: -1
ports_file_dir: %q
max_payload: 4096
tls {
 cert_file: %q
 key_file: %q
 ca_file: %q
 verify_and_map: true
 timeout: 2
}
authorization {
 users: [
  {user: "CN=node", permissions: {subscribe: ["papilio.test.v1.>"], allow_responses: {max: 1, expires: "5s"}}},
  {user: "CN=controller", permissions: {publish: ["papilio.test.v1.>"], subscribe: ["_INBOX.controller.>"]}},
  {user: "CN=outsider", permissions: {publish: ["elsewhere.>"], subscribe: ["_INBOX.outsider.>"]}}
 ]
}
`, directory, certificates.ServerCertificate, certificates.ServerKey, certificates.CA)
	path := filepath.Join(directory, "broker.conf")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(directory, "broker.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	root, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(root, binary, "-c", path)
	command.Stdout, command.Stderr = log, log
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.WaitDelay = time.Second
	if err := command.Start(); err != nil {
		cancel()
		log.Close()
		t.Fatal(err)
	}
	log.Close()
	broker := &NATSBroker{Certificates: certificates, cancel: cancel, done: make(chan struct{})}
	var waitErr error
	go func() { waitErr = command.Wait(); close(broker.done) }()
	t.Cleanup(broker.Close)
	portsFile := filepath.Join(directory, fmt.Sprintf("%s_%d.ports", filepath.Base(binary), command.Process.Pid))
	deadline, release := context.WithTimeout(context.Background(), 5*time.Second)
	defer release()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if data, err := os.ReadFile(portsFile); err == nil {
			var ports struct {
				NATS []string `json:"nats"`
			}
			if json.Unmarshal(data, &ports) == nil && len(ports.NATS) == 1 {
				broker.URL = ports.NATS[0]
				// PortsInfo uses the tls scheme for a TLS listener.
				return broker
			}
		}
		select {
		case <-broker.done:
			output, _ := os.ReadFile(logPath)
			t.Fatalf("isolated broker exited: %v\n%s", waitErr, output)
		case <-deadline.Done():
			broker.Close()
			output, _ := os.ReadFile(logPath)
			t.Fatalf("isolated broker did not bind: %s", output)
		case <-tick.C:
		}
	}
}

func (b *NATSBroker) Close() { b.cancel(); <-b.done }

func (b *NATSBroker) Config() settings.NATS {
	pair := b.Certificates.Clients["node"]
	return settings.NATS{
		Enabled: true, URL: b.URL, SubjectPrefix: "papilio.test.v1", QueueGroup: "test-node",
		RequestTimeout: time.Second, ConnectTimeout: time.Second, DrainTimeout: time.Second,
		MaxRequestBytes: 128, MaxResponseBytes: 4096, MaxPendingMessages: 8, MaxPendingBytes: 4096,
		TLS: settings.NATSClientTLS{CertificateFile: pair.Certificate, PrivateKeyFile: pair.Key, CAFile: b.Certificates.CA},
	}
}

func (b *NATSBroker) Client(t testing.TB, identity string, options ...nats.Option) *nats.Conn {
	t.Helper()
	base := []nats.Option{nats.Secure(b.Certificates.ClientTLS(t, identity)), nats.NoReconnect(), nats.Timeout(time.Second), nats.CustomInboxPrefix("_INBOX." + identity)}
	connection, err := nats.Connect(b.URL, append(base, options...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	return connection
}
