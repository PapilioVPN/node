//go:build integration && linux

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	instance "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	lifecycle "github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/instances"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/store"
	"github.com/xtls/xray-core/common/geodata"
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

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{root: privateDir(t)}
	f.target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "assets-native-traffic") }))
	t.Cleanup(f.target.Close)
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
	api := address(t)
	f.proxy = address(t)
	_, port, _ := net.SplitHostPort(f.proxy)
	_, apiport, _ := net.SplitHostPort(api)
	f.original = []byte(fmt.Sprintf(`{"log":{"loglevel":"warning"},"api":{"tag":"api","services":["StatsService"]},"stats":{},"inbounds":[{"tag":"proxy","listen":"127.0.0.1","port":%s,"protocol":"http","settings":{"accounts":[{"user":"asset-proof","pass":"private-password"}]}},{"tag":"api-in","listen":"127.0.0.1","port":%s,"protocol":"tunnel","settings":{"address":"127.0.0.1"}}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","inboundTag":["api-in"],"outboundTag":"api"},{"type":"field","domain":["geosite:PROOF"],"outboundTag":"direct"},{"type":"field","ip":["geoip:PROOF"],"outboundTag":"direct"}]}}`, port, apiport))
	f.config = filepath.Join(privateDir(t), "private.json")
	if err = os.WriteFile(f.config, f.original, 0600); err != nil {
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
