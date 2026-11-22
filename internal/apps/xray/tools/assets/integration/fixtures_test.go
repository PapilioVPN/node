//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	instance "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/tools/assets/grpcapi/proto/v1"
	lifecycle "github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/instances"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/store"
	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/xtls/xray-core/common/geodata"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func privateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func blob(data []byte) domain.File {
	sum := sha256.Sum256(data)
	return domain.File{Content: data, SHA256: hex.EncodeToString(sum[:])}
}
func candidate(t *testing.T, code, value string) domain.Candidate {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must name the verified pinned native executable")
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ip, err := proto.Marshal(&geodata.GeoIPList{Entry: []*geodata.GeoIP{{Code: code, Cidr: []*geodata.CIDR{{Ip: []byte{127, 0, 0, 0}, Prefix: 8}}}}})
	if err != nil {
		t.Fatal(err)
	}
	site, err := proto.Marshal(&geodata.GeoSiteList{Entry: []*geodata.GeoSite{{Code: code, Domain: []*geodata.Domain{{Type: geodata.Domain_Full, Value: value}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return domain.Candidate{Binary: blob(data), GeoIP: blob(ip), GeoSite: blob(site)}
}
func address(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

type startHook struct {
	instance.Runtime
	after func()
}

func (r startHook) Start(ctx context.Context) error {
	err := r.Runtime.Start(ctx)
	if err == nil && r.after != nil {
		r.after()
	}
	return err
}

type fixture struct {
	afterStart func()
	repo       *store.Store
	service    *services.Service
	runtime    *process.Process
	root       string
	config     string
	original   []byte
	proxy      string
	target     *httptest.Server
	gate       sync.Mutex // Fixture is the sole lifecycle caller; production must use Instances' slot.
}

func prepare(t *testing.T) (*fixture, string) {
	t.Helper()
	f := &fixture{root: privateDir(t)}
	f.target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "assets-native-traffic") }))
	t.Cleanup(f.target.Close)
	api := address(t)
	f.proxy = address(t)
	_, port, _ := net.SplitHostPort(f.proxy)
	_, apiport, _ := net.SplitHostPort(api)
	f.original = []byte(fmt.Sprintf(`{"log":{"loglevel":"warning"},"api":{"tag":"api","services":["StatsService"]},"stats":{},"inbounds":[{"tag":"proxy","listen":"127.0.0.1","port":%s,"protocol":"http","settings":{"accounts":[{"user":"asset-proof","pass":"private-password"}]}},{"tag":"api-in","listen":"127.0.0.1","port":%s,"protocol":"tunnel","settings":{"address":"127.0.0.1"}}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","inboundTag":["api-in"],"outboundTag":"api"},{"type":"field","domain":["geosite:PROOF"],"outboundTag":"direct"},{"type":"field","ip":["geoip:PROOF"],"outboundTag":"direct"}]}}`, port, apiport))
	f.config = filepath.Join(privateDir(t), "private.json")
	if err := os.WriteFile(f.config, f.original, 0600); err != nil {
		t.Fatal(err)
	}
	return f, api
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f, api := prepare(t)
	repo, err := store.Open(f.root, "one")
	if err != nil {
		t.Fatal(err)
	}
	f.repo = repo
	t.Cleanup(func() { _ = f.repo.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	base, err := repo.Stage(ctx, candidate(t, "PROOF", "base.test"))
	if err != nil {
		t.Fatal("stage baseline", err)
	}
	if err = repo.Initialize(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	conn, err := rpc.New(rpc.Config{Network: "tcp", Address: api})
	if err != nil {
		t.Fatal(err)
	}
	run, err := process.New(process.Config{ID: "one", Binary: filepath.Join(f.root, "current", "xray"), Configuration: f.config, StopTimeout: time.Second, API: conn})
	if err != nil {
		t.Fatal(err)
	}
	f.runtime = run
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := run.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	f.bind(t)
	if err = run.Start(ctx); err != nil {
		t.Fatal(err)
	}
	await(t, run)
	f.traffic(t)
	return f
}
func (f *fixture) bind(t *testing.T) {
	t.Helper()
	service, err := services.New([]services.Binding{{ID: "one", Repository: f.repo}}, lifecycle.Lifecycle{Acquire: func(ctx context.Context, id, executionID string) (instance.Runtime, func(), error) {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if !f.gate.TryLock() {
			return nil, nil, domain.ErrBusy
		}
		return startHook{Runtime: f.runtime, after: func() {
			if f.afterStart != nil {
				hook := f.afterStart
				f.afterStart = nil
				hook()
			}
		}}, f.gate.Unlock, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.service = service
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Error(err)
		}
	})
}
func await(t *testing.T, r *process.Process) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		probe, release := context.WithTimeout(ctx, 200*time.Millisecond)
		ok, err := r.Check(probe)
		release()
		if ok && err == nil {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatal("native readiness", r.Observe())
}
func (f *fixture) traffic(t *testing.T) {
	t.Helper()
	u, _ := url.Parse("http://" + f.proxy)
	u.User = url.UserPassword("asset-proof", "private-password")
	transport := &http.Transport{Proxy: http.ProxyURL(u), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get(f.target.URL)
	if err != nil {
		t.Fatal("native traffic", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || string(data) != "assets-native-traffic" {
		t.Fatal("native traffic response", response.StatusCode, string(data), err)
	}
	data, err = os.ReadFile(f.config)
	if err != nil || string(data) != string(f.original) {
		t.Fatal("private configuration changed", err)
	}
}
func (f *fixture) selection(t *testing.T) domain.Inventory {
	t.Helper()
	r, err := f.repo.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (f *fixture) activation(t *testing.T, id string) domain.Activation {
	t.Helper()
	r := f.selection(t)
	return domain.Activation{ExpectedRevision: r.State.Revision, ExecutionID: f.runtime.Observe().ExecutionID, ReleaseID: id}
}
func encode(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type eventLog struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (l *eventLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.data.Write(data)
}
func (l *eventLog) text() string { l.mu.Lock(); defer l.mu.Unlock(); return l.data.String() }
func assetBroker(t *testing.T, pki testutil.PKI) string {
	t.Helper()
	tls, err := mtls.Server(pki.Server)
	if err != nil {
		t.Fatal(err)
	}
	broker, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true, TLSConfig: tls, TLSTimeout: 2, TLSMap: true, TLSHandshakeFirst: true, MaxPayload: 64 << 20, MaxPending: 128 << 20, Users: []*server.User{{Username: "CN=client", Permissions: &server.Permissions{Publish: &server.SubjectPermission{Allow: []string{"fixture.>", "_INBOX.>"}}, Subscribe: &server.SubjectPermission{Allow: []string{"fixture.>", "_INBOX.>"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	go broker.Start()
	t.Cleanup(func() { broker.Shutdown(); broker.WaitForShutdown() })
	if !broker.ReadyForConnections(3 * time.Second) {
		t.Fatal("broker did not start")
	}
	return strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
}

type finalResources struct {
	fx.In
	Items []io.Closer `group:"final-resources"`
}
type controller struct {
	close     func()
	transport string
	http      *http.Client
	url       string
	grpc      *grpc.ClientConn
	nats      *nats.Conn
	logs      *eventLog
}

func management(t *testing.T, f *fixture, pki testutil.PKI, broker, transport string) *controller {
	t.Helper()
	if err := f.service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := settings.Config{StartupTimeout: 10 * time.Second, ShutdownTimeout: 45 * time.Second}
	cfg.Communications.Requests.Timeout = 20 * time.Second
	switch transport {
	case "http":
		cfg.Communications.HTTP = &settings.HTTP{Address: address(t), TLS: pki.Server, ReadTimeout: 20 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: domain.MaxRequest, Concurrency: 8}
	case "grpc":
		cfg.Communications.GRPC = &settings.GRPC{Address: address(t), TLS: pki.Server, ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxRecvMessageSize: domain.MaxRequest, MaxSendMessageSize: 1 << 20, MaxConnections: 8, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}
	case "nats":
		cfg.Communications.NATS = &settings.NATS{Servers: []string{broker}, TLS: pki.Client, TLSHandshakeFirst: true, ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: 10 * time.Second, ReconnectWait: time.Second, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 5}
		cfg.Communications.Requests.NATS = &settings.NATSRequests{SubjectPrefix: "fixture.assets", PendingMessages: 2, PendingBytes: 64 << 20, ShutdownTimeout: 10 * time.Millisecond}
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	c := &controller{transport: transport, logs: &eventLog{}}
	var resources []io.Closer
	app := bootstrap.New(cfg, slog.New(slog.NewJSONHandler(c.logs, nil)), assets.Module([]assets.Config{{ID: "one", Directory: f.root}}, cfg.Communications.Requests), fx.Provide(func() services.Lifecycle {
		return lifecycle.Lifecycle{Acquire: func(ctx context.Context, id, executionID string) (instance.Runtime, func(), error) {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			if !f.gate.TryLock() {
				return nil, nil, domain.ErrBusy
			}
			return f.runtime, f.gate.Unlock, nil
		}}
	}), fx.Invoke(func(in finalResources) { resources = in.Items }))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var stopOnce sync.Once
	c.close = func() {
		stopOnce.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if err := app.Stop(ctx); err != nil {
				t.Error(err)
			}
			for i := len(resources) - 1; i >= 0; i-- {
				if err := resources[i].Close(); err != nil {
					t.Error(err)
				}
			}
			if strings.Contains(c.logs.text(), "private-password") {
				t.Error("private state leaked into logs")
			}
		})
	}
	t.Cleanup(c.close)
	tls, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	switch transport {
	case "http":
		tr := &http.Transport{TLSClientConfig: tls}
		c.http = &http.Client{Transport: tr, Timeout: 35 * time.Second}
		t.Cleanup(tr.CloseIdleConnections)
		c.url = "https://" + cfg.Communications.HTTP.Address
		badTLS := tls.Clone()
		badTLS.Certificates = nil
		bad := &http.Transport{TLSClientConfig: badTLS}
		defer bad.CloseIdleConnections()
		if r, err := (&http.Client{Transport: bad, Timeout: time.Second}).Get(c.url + "/v1/xray/tools/instances/one/assets"); err == nil {
			r.Body.Close()
			t.Fatal("HTTP accepted unauthenticated controller")
		}
	case "grpc":
		c.grpc, err = grpc.NewClient(cfg.Communications.GRPC.Address, grpc.WithTransportCredentials(credentials.NewTLS(tls)), grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(domain.MaxRequest)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.grpc.Close() })
		badTLS := tls.Clone()
		badTLS.Certificates = nil
		bad, err := grpc.NewClient(cfg.Communications.GRPC.Address, grpc.WithTransportCredentials(credentials.NewTLS(badTLS)))
		if err != nil {
			t.Fatal(err)
		}
		defer bad.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := v1.NewAssetsServiceClient(bad).InspectAssets(ctx, &v1.InspectRequest{InstanceId: "one"}); err == nil {
			t.Fatal("gRPC accepted unauthenticated controller")
		}
	case "nats":
		c.nats, err = nats.Connect(broker, nats.Secure(tls), nats.TLSHandshakeFirst(), nats.NoReconnect(), nats.Timeout(2*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.nats.Close)
	}
	return c
}
func (c *controller) call(t *testing.T, op string, q map[string]any) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if c.transport == "grpc" {
		client := v1.NewAssetsServiceClient(c.grpc)
		var message proto.Message
		var err error
		switch op {
		case "inspect":
			message, err = client.InspectAssets(ctx, &v1.InspectRequest{InstanceId: "one"})
		case "stage":
			candidate := q["candidate"].(domain.Candidate)
			upload := func(f domain.File) *v1.Upload { return &v1.Upload{Content: f.Content, Sha256: f.SHA256} }
			message, err = client.StageAssets(ctx, &v1.StageRequest{InstanceId: "one", Candidate: &v1.Candidate{Binary: upload(candidate.Binary), Geoip: upload(candidate.GeoIP), Geosite: upload(candidate.GeoSite)}})
		default:
			revision := q["revision"].(uint64)
			execution := q["execution_id"].(string)
			switch op {
			case "activate":
				message, err = client.ActivateAssets(ctx, &v1.ActivateRequest{InstanceId: "one", ExpectedRevision: &revision, ExecutionId: execution, ReleaseId: q["release_id"].(string)})
			case "rollback":
				message, err = client.RollbackAssets(ctx, &v1.RecoveryRequest{InstanceId: "one", ExpectedRevision: &revision, ExecutionId: execution})
			case "recover":
				message, err = client.RecoverAssets(ctx, &v1.RecoveryRequest{InstanceId: "one", ExpectedRevision: &revision, ExecutionId: execution})
			}
		}
		if err != nil {
			t.Fatal("gRPC", op, err)
		}
		data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if q == nil {
		q = map[string]any{}
	}
	delete(q, "revision")
	if c.transport == "nats" {
		q["instance_id"] = "one"
		r, err := c.nats.RequestWithContext(ctx, "fixture.assets.xray.tools.assets.v1."+op, encode(t, q))
		if err != nil {
			t.Fatal("NATS", op, err)
		}
		return r.Data
	}
	path := "/v1/xray/tools/instances/one/assets"
	method := "POST"
	switch op {
	case "inspect":
		method = "GET"
	case "stage":
		path += "/candidates"
	case "activate":
		path += "/activations"
	case "rollback":
		path += "/rollbacks"
	case "recover":
		path += "/recoveries"
	}
	var body io.Reader
	if method != "GET" {
		body = bytes.NewReader(encode(t, q))
	}
	request, err := http.NewRequestWithContext(ctx, method, c.url+path, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	r, err := c.http.Do(request)
	if err != nil {
		t.Fatal("HTTP", op, err)
	}
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || r.StatusCode != 200 || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("HTTP response", op, r.StatusCode, string(data), err)
	}
	return data
}
func (c *controller) inspect(t *testing.T) domain.Inventory {
	t.Helper()
	var result domain.Inventory
	data := c.call(t, "inspect", nil)
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err, string(data))
	}
	if result.State.Active == "" {
		t.Fatal("missing installed assets", string(data))
	}
	return result
}
