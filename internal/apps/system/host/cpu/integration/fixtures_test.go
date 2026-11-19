//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

const subject = "fixture.node-a.system.host.load.v1.get"

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
	config := settings.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 35 * time.Second,
		Communications: settings.Communications{
			Requests: settings.Requests{Timeout: 200 * time.Millisecond, NATS: &settings.NATSRequests{SubjectPrefix: "fixture.node-a", PendingMessages: 8, PendingBytes: 8192, ShutdownTimeout: time.Second}},
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
		result.HTTP, result.HTTPAddress = &http.Client{Transport: transport, Timeout: time.Second}, "https://"+address+"/v1/system/host/load"
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
		addresses := make(map[string]string)
		loadSubscribed, timesSubscribed, usageSubscribed, infoSubscribed, frequencySubscribed, activitySubscribed := false, false, false, false, false, false
		for line := range strings.SplitSeq(docker(t, "logs", "--tail", "128", name), "\n") {
			var event struct {
				Message string `json:"msg"`
				Address string `json:"address"`
			}
			if json.Unmarshal([]byte(line), &event) == nil && event.Address != "" {
				addresses[event.Message] = event.Address
			}
			if event.Message == "host CPU activity NATS subscribed" {
				activitySubscribed = true
			}
			if event.Message == "host CPU frequency NATS subscribed" {
				frequencySubscribed = true
			}
			if event.Message == "host CPU info NATS subscribed" {
				infoSubscribed = true
			}
			if event.Message == "host CPU usage NATS subscribed" {
				usageSubscribed = true
			}
			if event.Message == "host CPU times NATS subscribed" {
				timesSubscribed = true
			}
			if event.Message == "host load NATS subscribed" {
				loadSubscribed = true
			}
		}
		if addresses["HTTP listening"] != "" && addresses["gRPC listening"] != "" && loadSubscribed && timesSubscribed && usageSubscribed && infoSubscribed && frequencySubscribed && activitySubscribed {
			return addresses
		}
		select {
		case <-ctx.Done():
			t.Fatal("packaged load endpoints did not start")
		case <-ticker.C:
		}
	}
}

func hostTickRate(t *testing.T) uint64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "getconf", "CLK_TCK").Output()
	if err != nil {
		t.Fatalf("read independent host tick rate: %v", err)
	}
	rate, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil || rate == 0 {
		t.Fatalf("invalid host tick rate: %s %v", output, err)
	}
	return rate
}

func hostCPUInfo(t *testing.T) map[uint32]map[string]string {
	t.Helper()
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		t.Fatal(err)
	}
	result := map[uint32]map[string]string{}
	for _, block := range strings.Split(string(data), "\n\n") {
		fields := map[string]string{}
		for line := range strings.SplitSeq(block, "\n") {
			key, value, ok := strings.Cut(line, ":")
			if ok {
				fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
		value, found := fields["processor"]
		if !found {
			continue
		}
		id, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		result[uint32(id)] = fields
	}
	return result
}

// This reference uses outside-Node raw sysfs attributes, not the Node adapter.
type frequencySource struct {
	text string
	err  error
}

func hostFrequencies(t *testing.T) (map[uint32]map[string]frequencySource, bool) {
	t.Helper()
	root := "/sys/devices/system/cpu/cpufreq"
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return map[uint32]map[string]frequencySource{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	result := map[uint32]map[string]frequencySource{}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "policy") {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimPrefix(entry.Name(), "policy"), 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		fields := map[string]frequencySource{}
		for _, name := range []string{"related_cpus", "affected_cpus", "cpuinfo_cur_freq", "cpuinfo_avg_freq", "cpuinfo_min_freq", "cpuinfo_max_freq", "scaling_cur_freq", "scaling_min_freq", "scaling_max_freq", "cpuinfo_transition_latency", "scaling_driver", "scaling_governor", "scaling_available_governors", "scaling_available_frequencies"} {
			data, err := os.ReadFile(filepath.Join(root, entry.Name(), name))
			fields[name] = frequencySource{text: strings.TrimSpace(string(data)), err: err}
		}
		result[uint32(id)] = fields
	}
	return result, true
}

func hostActivity(t *testing.T) map[string]uint64 {
	t.Helper()
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]uint64{}
	for line := range strings.SplitSeq(string(data), "\n") {
		key, values, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch key {
		case "ctxt", "intr", "softirq", "processes", "procs_running", "procs_blocked":
		default:
			continue
		}
		fields := strings.Fields(values)
		if len(fields) == 0 {
			t.Fatal("missing reference activity")
		}
		value, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		result[key] = value
	}
	if len(result) != 6 {
		t.Fatal("incomplete reference activity")
	}
	return result
}
