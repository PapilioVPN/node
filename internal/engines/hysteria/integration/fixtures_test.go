package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	grpcclient "github.com/PapilioVPN/go-bridge/api/grpc/client"
	bridgegrpc "github.com/PapilioVPN/go-bridge/api/grpc/server"
	bridgehttp "github.com/PapilioVPN/go-bridge/api/http/server"
	"github.com/PapilioVPN/node/internal/engines/hysteria"
	logsgrpc "github.com/PapilioVPN/node/internal/engines/hysteria/logs/bridge/grpc"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

const (
	instance    = "/v1/hysteria/instances/edge"
	apiKey      = "fixture-management-key"
	statsSecret = "fixture-native-stats-secret"
	password    = "fixture-client-password"
)

type fixture struct {
	dir, binary, udp, stats, cert, key string
	work                               string
	source                             []byte
	engine                             *hysteria.Engine
	http                               *httptest.Server
}

func setup(t testing.TB, auth string) *fixture {
	t.Helper()
	return setupWithExecutable(t, auth, nil)
}

func setupWithExecutable(t testing.TB, auth string, executable func(*fixture) string) *fixture {
	t.Helper()
	binary := os.Getenv("NODE_TEST_HYSTERIA")
	if binary == "" {
		t.Skip("NODE_TEST_HYSTERIA must point to the pinned official binary")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{dir: t.TempDir(), binary: binary, udp: udpAddress(t), stats: tcpAddress(t)}
	f.work = filepath.Join(f.dir, "runtime")
	if err := os.Mkdir(f.work, 0700); err != nil {
		t.Fatal(err)
	}
	f.cert, f.key = certificate(t, f.dir)
	if auth == "" {
		auth = "  type: password\n  password: " + password + "\n"
	}
	f.source = fmt.Appendf(nil, "# exact source bytes\nlisten: %s\ntls:\n  cert: %s\n  key: %s\nauth:\n%strafficStats:\n  listen: %s\n  secret: %s\n", f.udp, f.cert, f.key, auth, f.stats, statsSecret)
	config := filepath.Join(f.dir, "server.yaml")
	if err := os.WriteFile(config, f.source, 0600); err != nil {
		t.Fatal(err)
	}
	serverBinary := binary
	if executable != nil {
		serverBinary = executable(f)
	}
	f.engine, err = hysteria.New(hysteria.Config{Instances: []hysteria.InstanceConfig{{ID: "edge", Executable: serverBinary, ConfigFile: config, RuntimeDirectory: f.work}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := f.engine.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler { return bridgehttp.RequireAPIKey(apiKey, next) })
	f.engine.RegisterHTTP(router)
	f.http = httptest.NewServer(router)
	t.Cleanup(f.http.Close)
	return f
}

func certificate(t testing.TB, dir string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "test.crt"), filepath.Join(dir, "test.key")
	for _, file := range []struct {
		path, kind string
		data       []byte
	}{{certPath, "CERTIFICATE", der}, {keyPath, "PRIVATE KEY", pk}} {
		if err := os.WriteFile(file.path, pem.EncodeToMemory(&pem.Block{Type: file.kind, Bytes: file.data}), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return certPath, keyPath
}

func tcpAddress(t testing.TB) string {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func udpAddress(t testing.TB) string {
	t.Helper()
	l, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.LocalAddr().String()
}

func (f *fixture) request(t testing.TB, method, suffix string, body []byte, want int) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, method, f.http.URL+instance+suffix, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-API-Key", apiKey)
	resp, err := f.http.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s: got HTTP %d, want %d", method, suffix, resp.StatusCode, want)
	}
	return b
}

func (f *fixture) grpcConn(t testing.TB) *grpc.ClientConn {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(f.cert, f.key)
	if err != nil {
		t.Fatal(err)
	}
	pem, err := os.ReadFile(f.cert)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("fixture trust root not accepted")
	}
	server, err := bridgegrpc.NewServer(bridgegrpc.Config{APIKey: apiKey, TLS: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}, grpc.InTapHandle(logsgrpc.TransportContext))
	if err != nil {
		t.Fatal(err)
	}
	f.engine.RegisterGRPC(server)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close(); <-done })
	conn, err := grpcclient.NewClient(grpcclient.Config{Target: listener.Addr().String(), APIKey: apiKey, TLS: &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
