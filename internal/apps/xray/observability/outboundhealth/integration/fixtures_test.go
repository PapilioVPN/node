//go:build integration && linux

package integration_test

import (
	"context"
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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/app/services"
	healthv1 "github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/infra/jsonwire"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi/proto/v1"
	statsservices "github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/app/services"
	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	command "github.com/xtls/xray-core/app/observatory/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

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
	transport string
	config    settings.Config
	original  []byte
	address   string
	http      *http.Client
	grpc      healthv1.OutboundHealthServiceClient
	instances instancesv1.InstancesServiceClient
	nats      *nats.Conn
	native    *grpc.ClientConn
	stats     stats.StatsServiceClient
	target    *httptest.Server
	blocked   atomic.Bool
	gate      chan struct{}
	gateOnce  sync.Once
	probes    atomic.Int32
}

func newFixture(t *testing.T, pki testutil.PKI, broker, transport, mode string, extra int) *fixture {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select the verified executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reservation.Close() })
	managementReservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { managementReservation.Close() })
	f := &fixture{transport: transport, gate: make(chan struct{})}
	f.target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.probes.Add(1)
		if f.blocked.Load() {
			select {
			case <-f.gate:
			case <-r.Context().Done():
				return
			}
		}
		// A nonzero response time also verifies burst nanosecond vs millisecond units.
		timer := time.NewTimer(3 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(func() { f.unblock(); f.target.Close() })
	servicesList := []string{"StatsService"}
	if mode != "unsupported" {
		servicesList = append(servicesList, "ObservatoryService")
	}
	outbounds := []any{map[string]any{"tag": "probe-good", "protocol": "freedom"}, map[string]any{"tag": "probe-bad", "protocol": "blackhole"}}
	for i := 0; i < extra; i++ {
		outbounds = append(outbounds, map[string]any{"tag": fmt.Sprintf("probe-extra-%04d", i), "protocol": "freedom"})
	}
	conf := map[string]any{"log": map[string]any{"loglevel": "none"}, "api": map[string]any{"tag": "api", "listen": reservation.Addr().String(), "services": servicesList}, "stats": map[string]any{}, "outbounds": outbounds}
	switch mode {
	case "ordinary":
		conf["observatory"] = map[string]any{"subjectSelector": []string{"probe-"}, "probeURL": f.target.URL, "probeInterval": "1h", "enableConcurrency": true}
	case "burst":
		conf["burstObservatory"] = map[string]any{"subjectSelector": []string{"probe-"}, "pingConfig": map[string]any{"destination": f.target.URL, "interval": "1h", "sampling": 1, "timeout": "15s"}}
	case "empty":
		conf["observatory"] = map[string]any{"subjectSelector": []string{}, "probeURL": f.target.URL, "probeInterval": "1h"}
	}
	data, err := json.Marshal(conf)
	if err != nil {
		t.Fatal(err)
	}
	f.original = data
	file := filepath.Join(t.TempDir(), "xray.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	f.config = settings.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 10 * time.Second, Instances: []settings.Instance{{ID: "edge", Engine: "xray", StopTimeout: time.Second, Xray: &settings.Xray{Binary: binary, Configuration: file, API: &settings.XrayAPI{Network: "tcp", Address: reservation.Addr().String()}}}}}
	// These instances distinguish missing Node API configuration and an unreachable
	// endpoint while preserving real running engine lifecycles.
	f.config.Instances = append(f.config.Instances,
		settings.Instance{ID: "disabled", Engine: "xray", StopTimeout: time.Second, Xray: &settings.Xray{Binary: binary, Configuration: emptyConfig(t)}},
		settings.Instance{ID: "unreachable", Engine: "xray", StopTimeout: time.Second, Xray: &settings.Xray{Binary: binary, Configuration: emptyConfig(t), API: &settings.XrayAPI{Network: "tcp", Address: unreachableAddress(t)}}},
	)
	f.config.Communications.Requests.Timeout = 2 * time.Second
	switch transport {
	case "http":
		f.config.Communications.HTTP = &settings.HTTP{Address: managementReservation.Addr().String(), TLS: pki.Server, ReadTimeout: 3 * time.Second, WriteTimeout: 4 * time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: 64 << 10, Concurrency: 16}
	case "grpc":
		f.config.Communications.GRPC = &settings.GRPC{Address: managementReservation.Addr().String(), TLS: pki.Server, ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxRecvMessageSize: 64 << 10, MaxSendMessageSize: 2 << 20, MaxConnections: 8, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}
	case "nats":
		f.config.Communications.NATS = &settings.NATS{Servers: []string{broker}, TLS: pki.Client, TLSHandshakeFirst: true, ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second, ReconnectWait: time.Second, MaxReconnects: 0, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 2}
		f.config.Communications.Requests.NATS = &settings.NATSRequests{SubjectPrefix: "fixture.health", PendingMessages: 8, PendingBytes: 2 << 20, ShutdownTimeout: 10 * time.Millisecond}
	}
	if err := f.config.Validate(); err != nil {
		t.Fatal(err)
	}
	logs := &listenerLog{Handler: slog.NewTextHandler(io.Discard, nil), addresses: map[string]string{}}
	// Shared-file edits are reserved for integration. This is the exact pending
	// group module and focused status binding, supplied through bootstrap's native
	// extension point; the production capability Module is exercised unchanged.
	app := bootstrap.New(f.config, slog.New(logs), outboundhealth.Module(f.config.Communications.Requests), fx.Provide(func(s statsservices.StatusReader) services.StatusReader { return s }))
	reservation.Close()
	managementReservation.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	f.native, err = grpc.NewClient(f.config.Instances[0].Xray.API.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.native.Close() })
	f.stats = stats.NewStatsServiceClient(f.native)
	f.waitNative(t)
	identity, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	logs.mu.Lock()
	defer logs.mu.Unlock()
	switch transport {
	case "http":
		resource := &http.Transport{TLSClientConfig: identity}
		t.Cleanup(resource.CloseIdleConnections)
		f.http = &http.Client{Transport: resource, Timeout: 4 * time.Second}
		f.address = "https://" + logs.addresses["HTTP listening"]
	case "grpc":
		conn, err := grpc.NewClient(logs.addresses["gRPC listening"], grpc.WithTransportCredentials(credentials.NewTLS(identity)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		f.grpc = healthv1.NewOutboundHealthServiceClient(conn)
		f.instances = instancesv1.NewInstancesServiceClient(conn)
	case "nats":
		f.nats, err = nats.Connect(broker, nats.Secure(identity), nats.TLSHandshakeFirst(), nats.NoReconnect())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(f.nats.Close)
	}
	return f
}
func emptyConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "xray.json")
	if err := os.WriteFile(p, []byte(`{"log":{"loglevel":"none"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func unreachableAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.Addr().String()
	l.Close()
	return a
}
func (f *fixture) unblock() { f.gateOnce.Do(func() { close(f.gate) }) }
func (f *fixture) waitNative(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := f.stats.GetSysStats(ctx, &stats.SysStatsRequest{}); err == nil {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("native API not ready")
		}
	}
}

type observation struct {
	jsonwire.Metadata
	Status *jsonwire.Status `json:"status"`
	Error  string           `json:"error"`
}
type page struct {
	jsonwire.Metadata
	Statuses []jsonwire.Status `json:"statuses"`
	Token    string            `json:"next_page_token"`
	Total    uint32            `json:"total"`
	Error    string            `json:"error"`
}

func (f *fixture) get(t *testing.T, id, tag string, maxAge uint32) observation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var o observation
	switch f.transport {
	case "http":
		query := url.Values{"outbound_tag": {tag}, "max_sample_age_seconds": {fmt.Sprint(maxAge)}}
		response, err := f.http.Get(f.address + "/v1/xray/observability/instances/" + id + "/outbound-health/status?" + query.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("cache allowed")
		}
		if err := json.NewDecoder(response.Body).Decode(&o); err != nil {
			t.Fatal(err)
		}
		if (response.StatusCode == 200) != (o.Error == "") {
			t.Fatal(response.StatusCode, o)
		}
	case "grpc":
		r, err := f.grpc.GetOutboundStatus(ctx, &healthv1.GetOutboundStatusRequest{InstanceId: id, OutboundTag: &tag, MaxSampleAgeSeconds: maxAge})
		if err != nil {
			return observation{Error: status.Convert(err).Message()}
		}
		o.Metadata = fromProto(r.Observation)
		if r.Status != nil {
			s := fromStatus(r.Status)
			o.Status = &s
		}
	case "nats":
		data, _ := json.Marshal(map[string]any{"instance_id": id, "outbound_tag": tag, "max_sample_age_seconds": maxAge})
		msg, err := f.nats.RequestWithContext(ctx, "fixture.health.xray.observability.outboundhealth.v1.get", data)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(msg.Data, &o); err != nil {
			t.Fatal(err)
		}
	}
	return o
}
func (f *fixture) list(t *testing.T, token string, size uint32) page {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var p page
	switch f.transport {
	case "http":
		query := url.Values{"page_token": {token}, "page_size": {fmt.Sprint(size)}}
		response, err := f.http.Get(f.address + "/v1/xray/observability/instances/edge/outbound-health/statuses?" + query.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if err := json.NewDecoder(response.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
	case "grpc":
		r, err := f.grpc.ListOutboundStatuses(ctx, &healthv1.ListOutboundStatusesRequest{InstanceId: "edge", PageSize: size, PageToken: token})
		if err != nil {
			return page{Error: status.Convert(err).Message()}
		}
		p.Metadata = fromProto(r.Observation)
		p.Token = r.NextPageToken
		p.Total = r.Total
		p.Statuses = make([]jsonwire.Status, 0, len(r.Statuses))
		for _, s := range r.Statuses {
			p.Statuses = append(p.Statuses, fromStatus(s))
		}
	case "nats":
		data, _ := json.Marshal(map[string]any{"instance_id": "edge", "page_token": token, "page_size": size})
		msg, err := f.nats.RequestWithContext(ctx, "fixture.health.xray.observability.outboundhealth.v1.list", data)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(msg.Data, &p); err != nil {
			t.Fatal(err)
		}
	}
	return p
}
func fromProto(m *healthv1.ObservationMetadata) jsonwire.Metadata {
	return jsonwire.Metadata{InstanceID: m.InstanceId, Generation: m.Generation, Source: m.Source, ObservedAt: m.ObservedAt.AsTime(), DurationNS: m.DurationNs, MaxSampleAgeSeconds: m.MaxSampleAgeSeconds}
}
func fromStatus(s *healthv1.OutboundStatus) jsonwire.Status {
	v := jsonwire.Status{Tag: s.OutboundTag, Alive: s.Alive, DelayMS: s.DelayMs, LatencyMS: s.LatencyMs, LastSeenUnixSec: s.LastSeenUnixSec, LastTryUnixSec: s.LastTryUnixSec, Mode: s.Mode, SampleAgeSeconds: s.SampleAgeSeconds, Freshness: s.Freshness}
	if s.SampledAt != nil {
		at := s.SampledAt.AsTime()
		v.SampledAt = &at
	}
	if h := s.HealthPing; h != nil {
		v.HealthPing = &jsonwire.HealthPing{All: h.All, Fail: h.Fail, DeviationNS: h.DeviationNs, AverageNS: h.AverageNs, MaxNS: h.MaxNs, MinNS: h.MinNs}
	}
	return v
}
func (f *fixture) control(t *testing.T, operation string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	switch f.transport {
	case "http":
		method, path := http.MethodPost, "/executions"
		if operation == "stop" {
			method, path = http.MethodDelete, "/execution"
		}
		r, err := http.NewRequestWithContext(ctx, method, f.address+"/v1/core/engines/instances/edge"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := f.http.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			b, _ := io.ReadAll(response.Body)
			t.Fatal(response.Status, string(b))
		}
	case "grpc":
		var err error
		if operation == "stop" {
			_, err = f.instances.StopInstance(ctx, &instancesv1.StopInstanceRequest{InstanceId: "edge"})
		} else {
			_, err = f.instances.RestartInstance(ctx, &instancesv1.RestartInstanceRequest{InstanceId: "edge"})
		}
		if err != nil {
			t.Fatal(err)
		}
	case "nats":
		msg, err := f.nats.RequestWithContext(ctx, "fixture.health.core.engines.instances.v1."+operation, []byte(`{"instance_id":"edge"}`))
		if err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		if err := json.Unmarshal(msg.Data, &response); err != nil || response["error"] != nil {
			t.Fatal(string(msg.Data), err)
		}
	}
}
func (f *fixture) awaitStatus(t *testing.T, tag string) observation {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		o := f.get(t, "edge", tag, 0)
		if o.Error == "" && o.Status != nil {
			return o
		}
		if time.Now().After(deadline) {
			t.Fatalf("no native sample for %s: %+v", tag, o)
		}
		<-ticker.C
	}
}

// countConnection counts actual SDK invocations forwarded to the real engine.
// Other methods (readiness and management) use the fixture's original channel.
type countConnection struct {
	grpc.ClientConnInterface
	calls int
}

func (c *countConnection) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	if method == command.ObservatoryService_GetOutboundStatus_FullMethodName {
		c.calls++
	}
	return c.ClientConnInterface.Invoke(ctx, method, args, reply, opts...)
}

// The count test fixes the already-running generation locally. Restart fencing
// itself is exercised through real instance lifecycle on all three transports.
type fixedStatus struct{}

func (fixedStatus) Status(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 1}}, nil
}
