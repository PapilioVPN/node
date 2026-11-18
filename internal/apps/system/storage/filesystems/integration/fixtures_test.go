//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type listenerLog struct {
	slog.Handler
	mu        sync.Mutex
	addresses map[string]string
}

func (log *listenerLog) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "HTTP listening" || record.Message == "gRPC listening" {
		log.mu.Lock()
		defer log.mu.Unlock()
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "address" {
				log.addresses[record.Message] = attr.Value.String()
			}
			return true
		})
	}
	return nil
}

type clients struct {
	HTTP        *http.Client
	HTTPAddress string
	GRPC        *grpc.ClientConn
	NATS        *nats.Conn
}

func startConfiguredNode(t *testing.T, pki testutil.PKI, config settings.Config) clients {
	t.Helper()
	log := &listenerLog{Handler: slog.NewTextHandler(io.Discard, nil), addresses: make(map[string]string)}
	app := bootstrap.New(config, slog.New(log))
	ctx, cancel := context.WithTimeout(context.Background(), config.StartupTimeout)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Errorf("stop Node: %v", err)
		}
	})
	log.mu.Lock()
	defer log.mu.Unlock()
	return connectClients(t, pki.Client, config, log.addresses)
}

func nodeConfig(t *testing.T, pki testutil.PKI) settings.Config {
	t.Helper()
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	brokerURL := strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
	config := settings.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 25 * time.Second,
		Communications: settings.Communications{
			Requests: settings.Requests{Timeout: 500 * time.Millisecond, NATS: &settings.NATSRequests{SubjectPrefix: "fixture.node-a", PendingMessages: 8, PendingBytes: 8192, ShutdownTimeout: time.Second}},
			HTTP: &settings.HTTP{Address: "127.0.0.1:0", TLS: pki.Server,
				ReadTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: 2048, Concurrency: 16},
			GRPC: &settings.GRPC{Address: "127.0.0.1:0", TLS: pki.Server,
				ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second,
				MaxRecvMessageSize: 2048, MaxSendMessageSize: 1 << 20, MaxConnections: 16, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192},
			NATS: &settings.NATS{Servers: []string{brokerURL}, TLS: pki.Client, TLSHandshakeFirst: true,
				ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second,
				ReconnectWait: time.Second, MaxReconnects: 0, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 2},
		},
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	return config
}

func connectClients(t *testing.T, files mtls.Files, config settings.Config, addresses map[string]string) clients {
	t.Helper()
	tlsConfig, err := mtls.Client(files)
	if err != nil {
		t.Fatal(err)
	}
	var result clients
	if address := addresses["HTTP listening"]; address != "" {
		transport := &http.Transport{TLSClientConfig: tlsConfig}
		t.Cleanup(transport.CloseIdleConnections)
		result.HTTP, result.HTTPAddress = &http.Client{Transport: transport, Timeout: time.Second}, "https://"+address+"/v1/system/storage/filesystems"
	}
	if address := addresses["gRPC listening"]; address != "" {
		result.GRPC, err = grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { result.GRPC.Close() })
	}
	if config.Communications.NATS != nil {
		result.NATS, err = nats.Connect(config.Communications.NATS.Servers[0], nats.Secure(tlsConfig), nats.TLSHandshakeFirst(), nats.NoReconnect(), nats.Timeout(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(result.NATS.Close)
	}
	return result
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", args...)
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", args[0], err, output)
	}
	return string(output)
}

func containerAddresses(t *testing.T, name string) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		addresses := map[string]string{}
		list, usage := false, false
		for line := range strings.SplitSeq(docker(t, "logs", "--tail", "128", name), "\n") {
			var event struct {
				Message string `json:"msg"`
				Address string `json:"address"`
			}
			if json.Unmarshal([]byte(line), &event) == nil && event.Address != "" {
				addresses[event.Message] = event.Address
			}
			usage = usage || event.Message == "filesystem usage NATS subscribed"
			list = list || event.Message == "filesystem mounts NATS subscribed"
		}
		if addresses["HTTP listening"] != "" && addresses["gRPC listening"] != "" && usage && list {
			return addresses
		}
		select {
		case <-ctx.Done():
			t.Fatal("packaged filesystem endpoints did not start")
		case <-ticker.C:
		}
	}
}
