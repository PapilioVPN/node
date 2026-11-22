//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	logsv1 "github.com/PapilioVPN/node/internal/apps/xray/observability/logs/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/infra/jsonwire"
	output "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/procfs"
	"go.uber.org/fx"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/encoding/protojson"
)

const prefix = "fixture.logs"
const secret = "private-log-credential-6a4bd380"

type listenerLog struct {
	slog.Handler
	mu        sync.Mutex
	addresses map[string]string
}

func (l *listenerLog) Handle(_ context.Context, r slog.Record) error {
	if r.Message == "HTTP listening" || r.Message == "gRPC listening" {
		l.mu.Lock()
		defer l.mu.Unlock()
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "address" {
				l.addresses[r.Message] = a.Value.String()
			}
			return true
		})
	}
	return nil
}

type observedSource struct {
	output.OutputSource
	calls *atomic.Int64
}

func (s observedSource) ReadOutput(ctx context.Context, r output.OutputRequest) (output.OutputPage, error) {
	s.calls.Add(1)
	return s.OutputSource.ReadOutput(ctx, r)
}

type outputInputs struct {
	fx.In
	Sources []output.OutputBinding `group:"xray-instance-output"`
}
type outputResults struct {
	fx.Out
	Sources []output.OutputBinding `group:"xray-instance-output"`
}
type fixture struct {
	config settings.Config
	proxy  string
	pki    testutil.PKI
	broker string
}
type node struct {
	app     *fx.App
	http    *http.Client
	address string
	grpc    *grpc.ClientConn
	nats    *nats.Conn
	source  output.OutputSource
	calls   *atomic.Int64
	stopped bool
}

func newFixture(t *testing.T, pki testutil.PKI, broker, transport string) fixture {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select the verified standalone executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := listener.Addr().String()
	_, port, _ := net.SplitHostPort(proxy)
	number, _ := strconv.Atoi(port)
	dir := t.TempDir()
	path := filepath.Join(dir, secret+".json")
	configBytes, err := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "info"}, "inbounds": []any{map[string]any{"tag": "console", "listen": "127.0.0.1", "port": number, "protocol": "http", "settings": map[string]any{}}}, "outbounds": []any{map[string]any{"protocol": "blackhole", "tag": "sink"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, configBytes, 0600); err != nil {
		t.Fatal(err)
	}
	state, work := t.TempDir(), t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(work, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanRetainedFixture(t, state, path) })
	c := settings.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 25 * time.Second, Instances: []settings.Instance{{ID: "edge", Engine: "xray", StopTimeout: time.Second, Xray: &settings.Xray{Binary: binary, Configuration: path, StateDirectory: state, WorkDirectory: work}}}}
	c.Communications.Requests.Timeout = 2 * time.Second
	if transport == "http" || transport == "all" {
		c.Communications.HTTP = &settings.HTTP{Address: "127.0.0.1:0", TLS: pki.Server, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: 4096, Concurrency: 32}
	}
	if transport == "grpc" || transport == "all" {
		c.Communications.GRPC = &settings.GRPC{Address: "127.0.0.1:0", TLS: pki.Server, ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxRecvMessageSize: 4096, MaxSendMessageSize: 65536, MaxConnections: 16, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}
	}
	if transport == "nats" || transport == "all" {
		c.Communications.NATS = &settings.NATS{Servers: []string{broker}, TLS: pki.Client, TLSHandshakeFirst: true, ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second, ReconnectWait: time.Second, MaxReconnects: 0, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 2}
		c.Communications.Requests.NATS = &settings.NATSRequests{SubjectPrefix: prefix, PendingMessages: 8, PendingBytes: 65536, ShutdownTimeout: 100 * time.Millisecond}
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	listener.Close()
	return fixture{config: c, proxy: proxy, pki: pki, broker: broker}
}
func startNode(t *testing.T, f fixture) *node {
	t.Helper()
	n := &node{calls: &atomic.Int64{}}
	logger := &listenerLog{Handler: slog.NewTextHandler(io.Discard, nil), addresses: map[string]string{}}
	// Observe output reads through the same instances binding as ordinary startup.
	n.app = bootstrap.New(f.config, slog.New(logger), fx.Decorate(func(in outputInputs) outputResults {
		result := outputResults{}
		for _, b := range in.Sources {
			result.Sources = append(result.Sources, output.OutputBinding{ID: b.ID, Source: observedSource{b.Source, n.calls}})
		}
		return result
	}), fx.Invoke(func(in outputInputs) {
		for _, b := range in.Sources {
			if b.ID == "edge" {
				n.source = b.Source
			}
		}
	}))
	ctx, cancel := context.WithTimeout(context.Background(), f.config.StartupTimeout)
	defer cancel()
	if err := n.app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.stop(t) })
	tls, err := mtls.Client(f.pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	if a := logger.addresses["HTTP listening"]; a != "" {
		transport := &http.Transport{TLSClientConfig: tls, ForceAttemptHTTP2: false}
		t.Cleanup(transport.CloseIdleConnections)
		n.http = &http.Client{Transport: transport, Timeout: 4 * time.Second}
		n.address = "https://" + a
	}
	if a := logger.addresses["gRPC listening"]; a != "" {
		n.grpc, err = grpc.NewClient(a, grpc.WithTransportCredentials(credentials.NewTLS(tls)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { n.grpc.Close() })
	}
	if f.config.Communications.NATS != nil {
		n.nats, err = nats.Connect(f.broker, nats.Secure(tls), nats.TLSHandshakeFirst(), nats.NoReconnect(), nats.Timeout(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(n.nats.Close)
	}
	await(t, func() bool {
		c, err := net.DialTimeout("tcp", f.proxy, 50*time.Millisecond)
		if err == nil {
			c.Close()
			return true
		}
		return false
	})
	return n
}
func (n *node) stop(t *testing.T) {
	t.Helper()
	if n.stopped {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := n.app.Stop(ctx); err != nil {
		t.Error(err)
	}
	n.stopped = true
}
func broker(t *testing.T, pki testutil.PKI) string {
	t.Helper()
	b := testutil.NewNATSBroker(t, pki.Server, true)
	return strings.Replace(b.ClientURL(), "nats://", "tls://", 1)
}
func await(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("native condition did not become observable")
}
func traffic(t *testing.T, f fixture, host string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", f.proxy, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(c, "GET http://"+host+"/ HTTP/1.1\r\nHost: "+host+"\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, c)
}
func pull(t *testing.T, n *node, transport, cursor string, wait uint32) jsonwire.Page {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	data, err := request(ctx, n, transport, cursor, wait, nil)
	if err != nil {
		t.Fatal(err)
	}
	return decodePage(t, data)
}
func decodePage(t *testing.T, data []byte) jsonwire.Page {
	t.Helper()
	if strings.Contains(string(data), secret) || strings.Contains(string(data), ".invalid") || strings.Contains(string(data), "127.0.0.1") || strings.Contains(string(data), "/tmp/") {
		t.Fatal("native detail escaped redaction")
	}
	var result struct {
		jsonwire.Page
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.Error != "" {
		t.Fatal(string(data), err)
	}
	if result.NextCursor == "" || result.Source != "console" || len(result.Records) > 50 {
		t.Fatal("missing position/source or oversized page", string(data))
	}
	return result.Page
}
func request(ctx context.Context, n *node, transport, cursor string, wait uint32, headers nats.Header) ([]byte, error) {
	if transport == "grpc" {
		client := logsv1.NewLogsServiceClient(n.grpc)
		var p *logsv1.LogPage
		var err error
		if cursor == "" {
			p, err = client.TailLogs(ctx, &logsv1.TailLogsRequest{InstanceId: "edge"})
		} else {
			p, err = client.FollowLogs(ctx, &logsv1.FollowLogsRequest{InstanceId: "edge", Cursor: cursor, WaitMs: wait})
		}
		if err != nil {
			return nil, err
		}
		return protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(p)
	}
	if transport == "http" {
		path := n.address + "/v1/xray/observability/instances/edge/logs"
		if cursor != "" {
			path += "/updates?cursor=" + url.QueryEscape(cursor) + "&wait_ms=" + strconv.FormatUint(uint64(wait), 10)
		}
		r, err := http.NewRequestWithContext(ctx, "GET", path, nil)
		if err != nil {
			return nil, err
		}
		response, err := n.http.Do(r)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		return io.ReadAll(response.Body)
	}
	r := map[string]any{"instance_id": "edge"}
	subject := prefix + ".xray.observability.logs.v1.tail"
	if cursor != "" {
		r["cursor"], r["wait_ms"] = cursor, wait
		subject = prefix + ".xray.observability.logs.v1.follow"
	}
	data, _ := json.Marshal(r)
	reply, err := n.nats.RequestMsgWithContext(ctx, &nats.Msg{Subject: subject, Data: data, Header: headers})
	if err != nil {
		return nil, err
	}
	return reply.Data, nil
}
func restart(t *testing.T, n *node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := instancesv1.NewInstancesServiceClient(n.grpc).RestartInstance(ctx, &instancesv1.RestartInstanceRequest{InstanceId: "edge"}); err != nil {
		t.Fatal(err)
	}
}

func reconnect(t *testing.T, n *node, f fixture, transport string) {
	t.Helper()
	tls, err := mtls.Client(f.pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	switch transport {
	case "http":
		n.http.CloseIdleConnections()
	case "grpc":
		target := n.grpc.Target()
		n.grpc.Close()
		n.grpc, err = grpc.NewClient(target, grpc.WithTransportCredentials(credentials.NewTLS(tls)))
		if err != nil {
			t.Fatal(err)
		}
	case "nats":
		n.nats.Close()
		n.nats, err = nats.Connect(f.broker, nats.Secure(tls), nats.TLSHandshakeFirst(), nats.NoReconnect(), nats.Timeout(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(n.nats.Close)
	}
}

// This is a failure-path test cleanup only. The private record and command line
// must identify this fixture before a pidfd can signal its retained child.
func cleanRetainedFixture(t *testing.T, state, config string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state, "instance-execution.json"))
	if err != nil {
		return
	}
	var record struct {
		PID        int
		StartTicks uint64
		InstanceID string
	}
	if json.Unmarshal(data, &record) != nil || record.PID <= 0 || record.StartTicks == 0 || record.InstanceID != "edge" {
		return
	}
	fd, err := unix.PidfdOpen(record.PID, 0)
	if err != nil {
		return
	}
	defer unix.Close(fd)
	fs, err := procfs.NewFS("/proc")
	if err != nil {
		t.Error(err)
		return
	}
	process, err := fs.Proc(record.PID)
	if err != nil {
		return
	}
	stat, err := process.Stat()
	if err != nil || stat.Starttime != record.StartTicks {
		return
	}
	command, err := process.CmdLine()
	if err != nil || !slices.Contains(command, config) {
		return
	}
	if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil {
		t.Error("owned retained fixture could not stop")
		return
	}
	polls := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	if _, err := unix.Poll(polls, 2000); err != nil || polls[0].Revents == 0 {
		t.Error("owned retained fixture did not exit")
		return
	}
	var info unix.Siginfo
	unix.Waitid(unix.P_PIDFD, fd, &info, unix.WEXITED|unix.WNOHANG, nil)
}
