package bootstrap

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	bridgerpc "github.com/PapilioVPN/go-bridge/api/grpc/client"
	"github.com/PapilioVPN/node/internal/settings"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestRunServesAndStopsBothListeners(t *testing.T) {
	for _, mode := range []struct {
		name   string
		secure bool
		mutual bool
	}{
		{name: "local plaintext"},
		{name: "TLS with API key", secure: true},
		{name: "mutual TLS with API key", secure: true, mutual: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			httpAddress, grpcAddress := availableAddresses(t)
			config := settings.Management{HTTPAddress: httpAddress, GRPCAddress: grpcAddress}
			scheme := "http"
			httpClient := &http.Client{Timeout: time.Second}
			grpcConfig := bridgerpc.Config{Target: grpcAddress}
			grpcOptions := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
			if mode.secure {
				files, clientTLS := testTLS(t, mode.mutual)
				config.TLS = files
				config.APIKey = "test-key"
				scheme = "https"
				httpClient.Transport = &http.Transport{TLSClientConfig: clientTLS}
				grpcConfig.TLS = clientTLS
				grpcOptions = nil
			}
			defer httpClient.CloseIdleConnections()

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- Run(ctx, settings.Config{Management: config}, quietLogger())
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(6 * time.Second):
					t.Error("Node did not stop within its shutdown budget")
					return
				}
				for _, address := range []string{httpAddress, grpcAddress} {
					connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
					if err == nil {
						connection.Close()
						t.Errorf("listener still open at %s", address)
					}
				}
			})

			verifyCtx, stopVerify := context.WithTimeout(ctx, 4*time.Second)
			defer stopVerify()
			address := scheme + "://" + httpAddress + "/healthz"
			if mode.secure {
				checkHTTP(t, verifyCtx, httpClient, address, "", http.StatusUnauthorized)
			}
			checkHTTP(t, verifyCtx, httpClient, address, config.APIKey, http.StatusNoContent)

			if mode.secure {
				unauthenticated, err := bridgerpc.NewClient(grpcConfig, grpcOptions...)
				if err != nil {
					t.Fatal(err)
				}
				_, err = healthpb.NewHealthClient(unauthenticated).Check(verifyCtx, &healthpb.HealthCheckRequest{})
				if status.Code(err) != codes.Unauthenticated {
					t.Errorf("expected Unauthenticated, got %v", err)
				}
				unauthenticated.Close()
			}
			grpcConfig.APIKey = config.APIKey
			connection, err := bridgerpc.NewClient(grpcConfig, grpcOptions...)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			response, err := healthpb.NewHealthClient(connection).Check(verifyCtx, &healthpb.HealthCheckRequest{})
			if err != nil || response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
				t.Fatalf("gRPC readiness = %v, %v", response, err)
			}
			verifyHostUptime(t, verifyCtx, httpClient, scheme+"://"+httpAddress, config.APIKey, connection)

			if mode.mutual {
				serverOnlyTLS := grpcConfig.TLS.Clone()
				serverOnlyTLS.Certificates = nil
				client := &http.Client{
					Transport: &http.Transport{TLSClientConfig: serverOnlyTLS},
					Timeout:   time.Second,
				}
				defer client.CloseIdleConnections()
				response, err := client.Get(address)
				if err == nil {
					response.Body.Close()
					t.Fatal("HTTP accepted a client without an mTLS certificate")
				}
			}
		})
	}
}

func verifyHostUptime(t *testing.T, ctx context.Context, httpClient *http.Client, baseURL, key string, connection *grpc.ClientConn) {
	t.Helper()
	const method = "/papiliovpn.node.v1.HostService/GetUptime"
	before := procUptime(t)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/host/uptime", nil)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		request.Header.Set("X-API-Key", key)
		checkHTTP(t, ctx, httpClient, request.URL.String(), "", http.StatusUnauthorized)
	}
	response, err := httpClient.Do(request)
	var observed struct {
		Seconds *uint64 `json:"uptime_seconds"`
	}
	if err != nil {
		t.Error(err)
	} else {
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("HTTP uptime status = %d, want %d", response.StatusCode, http.StatusOK)
		} else if err := json.NewDecoder(response.Body).Decode(&observed); err != nil {
			t.Error(err)
		} else if observed.Seconds == nil {
			t.Error("HTTP uptime response omitted uptime_seconds")
		}
	}
	var rpcUptime wrapperspb.UInt64Value
	rpcErr := connection.Invoke(ctx, method, &emptypb.Empty{}, &rpcUptime)
	after := procUptime(t)
	if observed.Seconds != nil && (*observed.Seconds < before || *observed.Seconds > after) {
		t.Errorf("HTTP uptime = %d, want %d to %d", *observed.Seconds, before, after)
	}
	if rpcErr != nil || rpcUptime.GetValue() < before || rpcUptime.GetValue() > after {
		t.Errorf("gRPC uptime = %d, %v, want %d to %d", rpcUptime.GetValue(), rpcErr, before, after)
	}
}

func procUptime(t *testing.T) uint64 {
	t.Helper()
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		t.Fatal("empty /proc/uptime")
	}
	whole, _, _ := strings.Cut(fields[0], ".")
	seconds, err := strconv.ParseUint(whole, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return seconds
}

func TestBindFailureReleasesFirstListener(t *testing.T) {
	httpAddress, _ := availableAddresses(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	config := settings.Management{HTTPAddress: httpAddress, GRPCAddress: occupied.Addr().String()}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Run(ctx, settings.Config{Management: config}, quietLogger()); err == nil {
		t.Fatal("Node started with an occupied gRPC address")
	}
	listener, err := net.Listen("tcp", httpAddress)
	if err != nil {
		t.Fatalf("HTTP listener survived failed startup: %v", err)
	}
	listener.Close()
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func availableAddresses(t *testing.T) (string, string) {
	t.Helper()
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	return first.Addr().String(), second.Addr().String()
}

func checkHTTP(t *testing.T, ctx context.Context, client *http.Client, url, key string, want int) {
	t.Helper()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if key != "" {
			request.Header.Set("X-API-Key", key)
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode != want {
				t.Fatalf("HTTP status = %d, want %d", response.StatusCode, want)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(err)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func testTLS(t *testing.T, mutual bool) (settings.TLS, *tls.Config) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		BasicConstraintsValid: true,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := settings.TLS{
		CertFile: filepath.Join(dir, "cert.pem"),
		KeyFile:  filepath.Join(dir, "key.pem"),
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(files.CertFile, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("invalid test certificate")
	}
	config := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	if mutual {
		files.ClientCAFile = files.CertFile
		config.Certificates = []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}
	}
	return files, config
}
