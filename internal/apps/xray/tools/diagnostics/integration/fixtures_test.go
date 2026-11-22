//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	configservices "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	configprocess "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/state"
	instanceservices "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/services"
	instanceprocess "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/app/services"
	diagnosticsv1 "github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/infra/jsonwire"
	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	stats "github.com/xtls/xray-core/app/stats/command"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const secret = "diagnostic-private-password-key-123456"

type listenerLog struct {
	slog.Handler
	mu        sync.Mutex
	addresses map[string]string
}

func (l *listenerLog) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "address" {
			l.addresses[r.Message] = a.Value.String()
		}
		return true
	})
	return nil
}

type fixture struct {
	lifecycle *instanceservices.Service
	store     *state.Store
	http      *http.Client
	address   string
	grpc      diagnosticsv1.DiagnosticsServiceClient
	nats      *nats.Conn
	sources   []configs.Source
}

func newFixture(t *testing.T, pki testutil.PKI, broker, mode string) *fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select the verified executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "binary-missing" {
		original, err := os.Open(binary)
		if err != nil {
			t.Fatal(err)
		}
		defer original.Close()
		binary = filepath.Join(t.TempDir(), "xray-private-binary")
		target, err := os.OpenFile(binary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(target, original)
		closeErr := target.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatal(copyErr, closeErr)
		}
	}
	native, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { native.Close() })
	httpAddress, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { httpAddress.Close() })
	grpcAddress, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { grpcAddress.Close() })
	servicesList := []string{"StatsService", "ReflectionService", "HandlerService", "RoutingService"}
	if mode == "no-reflection" {
		servicesList = []string{"StatsService"}
	}
	if mode == "no-stats" {
		servicesList = []string{"ReflectionService"}
	}
	raw := map[string]any{"log": map[string]any{"loglevel": "none"}, "api": map[string]any{"tag": "api", "listen": native.Addr().String(), "services": servicesList}, "stats": map[string]any{}}
	// A real native outbound password remains private in source and durable state.
	// The separate baseline file is never overwritten by a diagnostic request.
	raw["outbounds"] = []any{map[string]any{"protocol": "freedom"}, map[string]any{"tag": "private-fixture-outbound", "protocol": "shadowsocks", "settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": 12345, "method": "aes-128-gcm", "password": secret}}}}}
	raw["remarks"] = secret
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	baseline := filepath.Join(t.TempDir(), "private-"+secret+".json")
	if err := os.WriteFile(baseline, data, 0600); err != nil {
		t.Fatal(err)
	}
	stateDirectory := t.TempDir()
	store, err := state.New(stateDirectory, "edge")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Open(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	f := &fixture{store: store, sources: []configs.Source{{Name: "private.json", Content: data}}}
	if _, err := store.Write(ctx, 0, f.sources); err != nil {
		t.Fatal(err)
	}
	var conn *rpc.Client
	if mode != "disabled" {
		address := native.Addr().String()
		if mode == "unreachable" {
			other, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address = other.Addr().String()
			other.Close()
		}
		conn, err = rpc.New(rpc.Config{Network: "tcp", Address: address})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
	}
	runtime, err := instanceprocess.New(instanceprocess.Config{Binary: binary, Configuration: baseline, StopTimeout: time.Second, API: conn, Source: configprocess.Resolver{Reader: store, WorkDirectory: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	f.lifecycle, err = instanceservices.New([]instanceservices.Binding{{ID: "edge", Engine: "xray", Runtime: runtime}})
	if err != nil {
		t.Fatal(err)
	}
	native.Close()
	if err := f.lifecycle.StartAll(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := f.lifecycle.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	metadata, err := configservices.NewState([]configservices.StateBinding{{ID: "edge", Repository: store}}, nil, f.lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { metadata.Close(context.Background()) })
	config := settings.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second}
	config.Communications.Requests.Timeout = 2 * time.Second
	config.Communications.HTTP = &settings.HTTP{Address: httpAddress.Addr().String(), TLS: pki.Server, ReadTimeout: 3 * time.Second, WriteTimeout: 4 * time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: 4096, Concurrency: 8}
	config.Communications.GRPC = &settings.GRPC{Address: grpcAddress.Addr().String(), TLS: pki.Server, ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxRecvMessageSize: 4096, MaxSendMessageSize: 64 << 10, MaxConnections: 8, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}
	config.Communications.NATS = &settings.NATS{Servers: []string{broker}, TLS: pki.Client, TLSHandshakeFirst: true, ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second, ReconnectWait: time.Second, MaxReconnects: 0, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 2}
	config.Communications.Requests.NATS = &settings.NATSRequests{SubjectPrefix: "fixture.diagnostics", PendingMessages: 8, PendingBytes: 64 << 10, ShutdownTimeout: 10 * time.Millisecond}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	spec := settings.Instance{ID: "edge", Engine: "xray", Xray: &settings.Xray{Binary: binary, Configuration: baseline, StateDirectory: stateDirectory}}
	if conn != nil {
		spec.Xray.API = &settings.XrayAPI{Network: "tcp", Address: native.Addr().String()}
	}
	logs := &listenerLog{Handler: slog.NewTextHandler(io.Discard, nil), addresses: map[string]string{}}
	app := bootstrap.New(config, slog.New(logs), diagnostics.Module([]settings.Instance{spec}, config.Communications.Requests),
		fx.Provide(func() services.StatusReader { return f.lifecycle }, func() services.MetadataReader { return metadata }),
		fx.Provide(fx.Annotate(func() []rpc.Binding { return []rpc.Binding{{ID: "edge", Client: conn}} }, fx.ResultTags(`group:"xray-native-api,flatten"`))))
	httpAddress.Close()
	grpcAddress.Close()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	// Await the actual test engine endpoint, independently from the deliberately
	// unreachable/disabled Node binding. Use one reused test-only native channel.
	nativeConn, err := grpc.NewClient(native.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nativeConn.Close() })
	if mode != "no-stats" {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := stats.NewStatsServiceClient(nativeConn).GetSysStats(ctx, &stats.SysStatsRequest{}); err == nil {
				break
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatal("native API not ready")
			}
		}
	}
	if mode == "no-stats" {
		reader := api.New(nativeConn)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			value, err := reader.Read(ctx)
			if err == nil && value.Discovery.State == "available" {
				break
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatal("native reflection not ready")
			}
		}
	}
	if mode == "binary-missing" {
		if err := os.Remove(binary); err != nil {
			t.Fatal(err)
		}
	}
	f.connect(t, pki, broker, logs)
	return f
}

func (f *fixture) connect(t *testing.T, pki testutil.PKI, broker string, logs *listenerLog) {
	t.Helper()
	identity, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	logs.mu.Lock()
	defer logs.mu.Unlock()
	tr := &http.Transport{TLSClientConfig: identity}
	t.Cleanup(tr.CloseIdleConnections)
	f.http = &http.Client{Transport: tr, Timeout: 4 * time.Second}
	f.address = "https://" + logs.addresses["HTTP listening"]
	grpcConn, err := grpc.NewClient(logs.addresses["gRPC listening"], grpc.WithTransportCredentials(credentials.NewTLS(identity)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { grpcConn.Close() })
	f.grpc = diagnosticsv1.NewDiagnosticsServiceClient(grpcConn)
	f.nats, err = nats.Connect(broker, nats.Secure(identity), nats.TLSHandshakeFirst(), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.nats.Close)
}
func (f *fixture) report(t *testing.T, transport string) jsonwire.Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var r jsonwire.Report
	var data []byte
	switch transport {
	case "http":
		response, err := f.http.Get(f.address + "/v1/xray/tools/instances/edge/diagnostics")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err = io.ReadAll(response.Body)
		if err != nil || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal(response.Status, string(data), err)
		}
	case "nats":
		msg, err := f.nats.RequestWithContext(ctx, "fixture.diagnostics.xray.tools.diagnostics.v1.get", []byte(`{"instance_id":"edge"}`))
		if err != nil {
			t.Fatal(err)
		}
		data = msg.Data
	case "grpc":
		response, err := f.grpc.GetInstanceDiagnostics(ctx, &diagnosticsv1.GetInstanceDiagnosticsRequest{InstanceId: "edge"})
		if err != nil {
			t.Fatal(err)
		}
		binary, err := proto.Marshal(response)
		if err != nil || len(binary) > 32<<10 || strings.Contains(string(binary), secret) {
			t.Fatal("unsafe protobuf", err)
		}
		data, err = protojson.MarshalOptions{UseProtoNames: true}.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		// The JSON API flattens Evidence, while protobuf keeps typed nested messages.
		checkProto(t, response)
		e, c, n := response.Execution, response.Configuration, response.NativeApi
		return jsonwire.Report{InstanceID: response.InstanceId, Consistency: response.Consistency, Execution: jsonwire.Execution{Generation: e.Generation, AppliedRevision: e.AppliedRevision, LifecycleState: e.LifecycleState}, Configuration: jsonwire.Configuration{Evidence: jsonwire.Evidence{State: c.Evidence.State, Reason: c.Evidence.Reason}, DesiredRevision: c.DesiredRevision, MatchesApplied: c.MatchesApplied}, Binary: jsonwire.Binary{Evidence: jsonwire.Evidence{State: response.Binary.Evidence.State, Reason: response.Binary.Evidence.Reason}, Version: response.Binary.Version, Build: response.Binary.Build, Scope: response.Binary.Scope}, Native: jsonwire.Native{Evidence: jsonwire.Evidence{State: n.Evidence.State}, Discovery: jsonwire.Evidence{State: n.Discovery.State}, Runtime: jsonwire.Evidence{State: n.Runtime.State}, Capabilities: protoCapabilities(n.Capabilities)}}
	}
	if strings.Contains(string(data), secret) || len(data) > 32<<10 {
		t.Fatal("unsafe diagnostic report")
	}
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err, string(data))
	}
	return r
}
func protoCapabilities(values []*diagnosticsv1.Capability) []jsonwire.Capability {
	var result []jsonwire.Capability
	for _, v := range values {
		result = append(result, jsonwire.Capability{Name: v.Name, State: v.State, Basis: v.Basis})
	}
	return result
}
func checkProto(t *testing.T, r *diagnosticsv1.DiagnosticReport) {
	t.Helper()
	data, err := protojson.Marshal(r)
	if err != nil || strings.Contains(string(data), secret) || r.ObservedAt == nil {
		t.Fatal("unsafe protobuf JSON", err)
	}
}
