//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	instancesv1 "github.com/PapilioVPN/node/internal/apps/core/engines/instances/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const prefix = "fixture.instances"
const root = "/v1/core/engines/instances/"

type listenerLog struct {
	slog.Handler
	mu        sync.Mutex
	addresses map[string]string
}

func (log *listenerLog) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "HTTP listening" || record.Message == "gRPC listening" {
		log.mu.Lock()
		defer log.mu.Unlock()
		record.Attrs(func(a slog.Attr) bool {
			if a.Key == "address" {
				log.addresses[record.Message] = a.Value.String()
			}
			return true
		})
	}
	return nil
}

type observation struct {
	ID              string `json:"instance_id"`
	State           string `json:"state"`
	Generation      string `json:"generation"`
	Forced          bool   `json:"forced"`
	ExecutionFailed bool   `json:"execution_failed"`
	ExitCode        *int32 `json:"exit_code"`
}
type control struct {
	Status observation `json:"status"`
	Effect string      `json:"effect"`
	Error  string      `json:"error"`
}
type readiness struct {
	APIState     string `json:"api_state"`
	TrafficState string `json:"traffic_state"`
	Error        string `json:"error"`
}
type clients struct {
	http    *http.Client
	address string
	grpc    *grpc.ClientConn
	nats    *nats.Conn
}

type engine struct {
	settings settings.Instance
	proxy    string
	api      string
	original []byte
}

func engineFixture(t *testing.T, id string) engine {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select the verified standalone executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	// Reserve both child endpoints before releasing either; no later test listener
	// is allocated between these reservations and the selected native startup.
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	apiListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		proxyListener.Close()
		t.Fatal(err)
	}
	proxy, api := proxyListener.Addr().String(), apiListener.Addr().String()
	_, port, _ := net.SplitHostPort(proxy)
	numericPort, _ := strconv.Atoi(port)
	data, err := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "none"}, "api": map[string]any{"tag": "api", "listen": api, "services": []string{"StatsService"}}, "stats": map[string]any{}, "inbounds": []any{map[string]any{"tag": "proxy", "listen": "127.0.0.1", "port": numericPort, "protocol": "http", "settings": map[string]any{}}}, "outbounds": []any{map[string]any{"protocol": "freedom", "tag": "direct"}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "xray.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	proxyListener.Close()
	apiListener.Close()
	return engine{settings.Instance{ID: id, Engine: "xray", StopTimeout: 500 * time.Millisecond, Xray: &settings.Xray{Binary: binary, Configuration: path, API: &settings.XrayAPI{Network: "tcp", Address: api}}}, proxy, api, data}
}
func nodeConfig(t *testing.T, pki testutil.PKI, brokerURL, transport string, engines ...engine) settings.Config {
	t.Helper()
	config := settings.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 12 * time.Second}
	config.Communications.Requests.Timeout = 2 * time.Second
	for _, engine := range engines {
		config.Instances = append(config.Instances, engine.settings)
	}
	if transport == "all" || transport == "http" {
		config.Communications.HTTP = &settings.HTTP{Address: "127.0.0.1:0", TLS: pki.Server, ReadTimeout: 3 * time.Second, WriteTimeout: 4 * time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: 4096, Concurrency: 32}
	}
	if transport == "all" || transport == "grpc" {
		config.Communications.GRPC = &settings.GRPC{Address: "127.0.0.1:0", TLS: pki.Server, ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxRecvMessageSize: 4096, MaxSendMessageSize: 8192, MaxConnections: 16, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}
	}
	if transport == "all" || transport == "nats" {
		config.Communications.NATS = &settings.NATS{Servers: []string{brokerURL}, TLS: pki.Client, TLSHandshakeFirst: true, ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second, ReconnectWait: time.Second, MaxReconnects: 0, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 2}
		config.Communications.Requests.NATS = &settings.NATSRequests{SubjectPrefix: prefix, PendingMessages: 16, PendingBytes: 65536, ShutdownTimeout: 100 * time.Millisecond}
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	return config
}
func startNode(t *testing.T, pki testutil.PKI, config settings.Config) clients {
	t.Helper()
	logger := &listenerLog{Handler: slog.NewTextHandler(io.Discard, nil), addresses: map[string]string{}}
	app := bootstrap.New(config, slog.New(logger))
	ctx, cancel := context.WithTimeout(context.Background(), config.StartupTimeout)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	logger.mu.Lock()
	defer logger.mu.Unlock()
	return connectClients(t, pki.Client, config, logger.addresses)
}
func connectClients(t *testing.T, identity mtls.Files, config settings.Config, addresses map[string]string) clients {
	t.Helper()
	tls, err := mtls.Client(identity)
	if err != nil {
		t.Fatal(err)
	}
	var result clients
	if address := addresses["HTTP listening"]; address != "" {
		transport := &http.Transport{TLSClientConfig: tls}
		t.Cleanup(transport.CloseIdleConnections)
		result.http = &http.Client{Transport: transport, Timeout: 4 * time.Second}
		result.address = "https://" + address
	}
	if address := addresses["gRPC listening"]; address != "" {
		result.grpc, err = grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(tls)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { result.grpc.Close() })
	}
	if config.Communications.NATS != nil {
		result.nats, err = nats.Connect(config.Communications.NATS.Servers[0], nats.Secure(tls), nats.TLSHandshakeFirst(), nats.NoReconnect(), nats.Timeout(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(result.nats.Close)
	}
	return result
}
func (c clients) jsonRequest(ctx context.Context, transport, operation, id string) ([]byte, error) {
	if transport == "http" {
		method, path := "GET", root+id
		switch operation {
		case "start":
			method, path = "PUT", path+"/execution"
		case "stop":
			method, path = "DELETE", path+"/execution"
		case "restart":
			method, path = "POST", path+"/executions"
		case "readiness":
			path += "/readiness"
		}
		request, _ := http.NewRequestWithContext(ctx, method, c.address+path, nil)
		response, err := c.http.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != 200 {
			return data, errors.New(string(data))
		}
		return data, nil
	}
	suffix := "core.engines.instances.v1." + operation
	if operation == "status" || operation == "readiness" {
		suffix = "core.engines.instances." + operation + ".v1.get"
	}
	data, _ := json.Marshal(map[string]string{"instance_id": id})
	message, err := c.nats.RequestWithContext(ctx, prefix+"."+suffix, data)
	if err != nil {
		return nil, err
	}
	var response struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(message.Data, &response) != nil || response.Error != "" {
		return message.Data, errors.New(string(message.Data))
	}
	return message.Data, nil
}
func (c clients) control(ctx context.Context, transport, operation, id string) (control, error) {
	if transport != "grpc" {
		data, err := c.jsonRequest(ctx, transport, operation, id)
		var result control
		if err != nil {
			return result, err
		}
		err = json.Unmarshal(data, &result)
		return result, err
	}
	client := instancesv1.NewInstancesServiceClient(c.grpc)
	var result *instancesv1.ControlResult
	var err error
	switch operation {
	case "start":
		result, err = client.StartInstance(ctx, &instancesv1.StartInstanceRequest{InstanceId: id})
	case "stop":
		result, err = client.StopInstance(ctx, &instancesv1.StopInstanceRequest{InstanceId: id})
	case "restart":
		result, err = client.RestartInstance(ctx, &instancesv1.RestartInstanceRequest{InstanceId: id})
	}
	if err != nil {
		return control{}, err
	}
	return control{Status: grpcObservation(result.Status), Effect: result.Effect}, nil
}
func grpcObservation(s *instancesv1.InstanceStatus) observation {
	return observation{ID: s.InstanceId, State: s.State, Generation: strconv.FormatUint(s.Generation, 10), Forced: s.Forced, ExecutionFailed: s.ExecutionFailed, ExitCode: s.ExitCode}
}
func (c clients) status(ctx context.Context, transport, id string) (observation, error) {
	if transport == "grpc" {
		result, err := instancesv1.NewInstancesServiceClient(c.grpc).GetInstanceStatus(ctx, &instancesv1.GetInstanceStatusRequest{InstanceId: id})
		if err != nil {
			return observation{}, err
		}
		return grpcObservation(result), nil
	}
	data, err := c.jsonRequest(ctx, transport, "status", id)
	var result observation
	if err == nil {
		err = json.Unmarshal(data, &result)
	}
	return result, err
}
func (c clients) ready(ctx context.Context, transport, id string) error {
	if transport == "grpc" {
		result, err := instancesv1.NewInstancesServiceClient(c.grpc).CheckInstanceReadiness(ctx, &instancesv1.CheckInstanceReadinessRequest{InstanceId: id})
		if err != nil {
			return err
		}
		if result.ApiState != "ready" || result.TrafficState != "not_checked" {
			return errors.New("not ready")
		}
		return nil
	}
	data, err := c.jsonRequest(ctx, transport, "readiness", id)
	if err != nil {
		return err
	}
	var result readiness
	if err = json.Unmarshal(data, &result); err != nil {
		return err
	}
	if result.APIState != "ready" || result.TrafficState != "not_checked" {
		return errors.New("not ready")
	}
	return nil
}
func awaitReady(t *testing.T, client clients, transport, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := client.ready(ctx, transport, id); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("native API did not become ready")
		case <-ticker.C:
		}
	}
}
func traffic(address, target string) error {
	proxy, _ := url.Parse("http://" + address)
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: time.Second}).Get(target)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if string(data) != "instance-traffic-proof" {
		return errors.New("unexpected proxy response")
	}
	return nil
}
func brokerFixture(t *testing.T, pki testutil.PKI) string {
	t.Helper()
	return strings.Replace(testutil.NewNATSBroker(t, pki.Server, true).ClientURL(), "nats://", "tls://", 1)
}
