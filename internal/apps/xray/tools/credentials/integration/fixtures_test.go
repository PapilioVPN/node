//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	instanceservices "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/services"
	instanceprocess "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
	credentialsv1 "github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics"
	diagnosticservices "github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/app/services"
	diagnosticsv1 "github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	grpccredentials "google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func nativeService(t *testing.T) *services.Service {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select the pinned Xray executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	executor := process.NewExecutor()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if executor.Close(ctx) != nil {
			t.Error("credential owner failed to close")
		}
	})
	directory := t.TempDir()
	runner, err := process.New(process.Config{Binary: binary, Directory: directory, WorkDirectory: directory}, executor)
	if err != nil {
		t.Fatal(err)
	}
	service, err := services.New([]services.Binding{{ID: "a", Generator: runner}})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
func ptr[T any](value T) *T { return &value }

func echBytes(t *testing.T, value string, format domain.ECHFormat) []byte {
	t.Helper()
	if format == domain.PEM {
		block, rest := pem.Decode([]byte(value))
		if block == nil || len(rest) != 0 {
			t.Fatal("invalid native ECH PEM")
		}
		return block.Bytes
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal("invalid native ECH base64")
	}
	return raw
}
func goECHKeys(t *testing.T, raw []byte) []tls.EncryptedClientHelloKey {
	t.Helper()
	var keys []tls.EncryptedClientHelloKey
	read := func() []byte {
		if len(raw) < 2 {
			t.Fatal("truncated ECH length")
		}
		n := int(binary.BigEndian.Uint16(raw))
		raw = raw[2:]
		if n > len(raw) {
			t.Fatal("truncated ECH vector")
		}
		value := raw[:n]
		raw = raw[n:]
		return value
	}
	for len(raw) > 0 {
		private := read()
		config := read()
		keys = append(keys, tls.EncryptedClientHelloKey{PrivateKey: private, Config: config})
	}
	return keys
}
func echHandshake(t *testing.T, certificate domain.CertificateResult, ech domain.ECHResult) {
	t.Helper()
	pair, err := tls.X509KeyPair([]byte(certificate.CertificatePEM), []byte(certificate.PrivateKeyPEM))
	if err != nil {
		t.Fatal("certificate unusable")
	}
	keys := goECHKeys(t, echBytes(t, ech.ServerKeys, ech.Format))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(certificate.CertificatePEM)) {
		t.Fatal("certificate root unusable")
	}
	serverConfig := &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13, EncryptedClientHelloKeys: keys}
	joined := make(chan error, 1)
	go func() {
		for range ech.ConfigLists {
			conn, err := listener.Accept()
			if err != nil {
				joined <- err
				return
			}
			conn.SetDeadline(time.Now().Add(2 * time.Second))
			server := tls.Server(conn, serverConfig)
			err = server.Handshake()
			if err == nil {
				_, err = server.Write([]byte("ech-proof"))
			}
			server.Close()
			if err != nil {
				joined <- err
				return
			}
		}
		joined <- nil
	}()
	for _, list := range ech.ConfigLists {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		config := &tls.Config{RootCAs: roots, ServerName: "private.example.test", MinVersion: tls.VersionTLS13, EncryptedClientHelloConfigList: echBytes(t, list, ech.Format)}
		conn, err := (&tls.Dialer{Config: config}).DialContext(ctx, "tcp", listener.Addr().String())
		cancel()
		if err != nil {
			listener.Close()
			<-joined
			t.Fatal("ECH handshake failed")
		}
		tlsConn := conn.(*tls.Conn)
		if !tlsConn.ConnectionState().ECHAccepted {
			conn.Close()
			listener.Close()
			<-joined
			t.Fatal("ECH was not accepted")
		}
		data := make([]byte, len("ech-proof"))
		_, err = io.ReadFull(conn, data)
		conn.Close()
		if err != nil || string(data) != "ech-proof" {
			listener.Close()
			<-joined
			t.Fatal("ECH protected payload failed")
		}
	}
	if err := <-joined; err != nil {
		t.Fatal("ECH server failed")
	}
}

type logBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *logBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(data)
}
func (b *logBuffer) text() string { b.mu.Lock(); defer b.mu.Unlock(); return b.Buffer.String() }

type boundaryFixture struct {
	transport string
	http      *http.Client
	address   string
	grpc      *grpc.ClientConn
	nats      *nats.Conn
	logs      *logBuffer
	work      string
}

func boundary(t *testing.T, pki testutil.PKI, broker, transport string) *boundaryFixture {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY required")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	work := t.TempDir()
	path := filepath.Join(directory, "engine.json")
	if os.WriteFile(path, []byte(`{"log":{"loglevel":"none"},"outbounds":[{"protocol":"blackhole"}]}`), 0600) != nil {
		t.Fatal("cannot write isolated engine source")
	}
	native, err := instanceprocess.New(instanceprocess.Config{Binary: binary, Configuration: path, StopTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := instanceservices.New([]instanceservices.Binding{{ID: "a", Engine: "xray", Runtime: native}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lifecycle.StartAll(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if lifecycle.Close(ctx) != nil {
			t.Error("native runtime failed to join")
		}
	})
	specs := []settings.Instance{
		{ID: "a", Engine: "xray", Xray: &settings.Xray{Binary: binary, Configuration: path, WorkDirectory: work}},
		{ID: "b", Engine: "xray", Xray: &settings.Xray{Binary: binary, Configuration: path}},
		{ID: "missing-binary", Engine: "xray", Xray: &settings.Xray{Binary: filepath.Join(directory, "missing"), Configuration: path}},
	}
	config := settings.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 8 * time.Second}
	config.Communications.Requests.Timeout = 2 * time.Second
	switch transport {
	case "http":
		config.Communications.HTTP = &settings.HTTP{Address: "127.0.0.1:0", TLS: pki.Server, ReadTimeout: time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: domain.MaxRequestBytes, Concurrency: 32}
	case "grpc":
		config.Communications.GRPC = &settings.GRPC{Address: "127.0.0.1:0", TLS: pki.Server, ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxRecvMessageSize: domain.MaxRequestBytes, MaxSendMessageSize: domain.MaxOutputBytes, MaxConnections: 8, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}
	case "nats":
		config.Communications.NATS = &settings.NATS{Servers: []string{broker}, TLS: pki.Client, TLSHandshakeFirst: true, ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second, ReconnectWait: time.Second, MaxReconnects: 0, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 2}
		config.Communications.Requests.NATS = &settings.NATSRequests{SubjectPrefix: "fixture.credentials", PendingMessages: 8, PendingBytes: domain.MaxRequestBytes, ShutdownTimeout: 10 * time.Millisecond}
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	f := &boundaryFixture{transport: transport, logs: &logBuffer{}, work: work}
	logger := slog.New(slog.NewJSONHandler(f.logs, nil))
	app := bootstrap.New(config, logger, credentials.Module(specs, config.Communications.Requests), diagnostics.Module(specs, config.Communications.Requests), fx.Provide(func() diagnosticservices.StatusReader { return lifecycle }))
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		if app.Stop(ctx) != nil {
			t.Error("management runtime failed to stop")
		}
	})
	addresses := map[string]string{}
	for line := range strings.SplitSeq(f.logs.text(), "\n") {
		var event struct {
			Message string `json:"msg"`
			Address string `json:"address"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && event.Address != "" {
			addresses[event.Message] = event.Address
		}
	}
	tlsConfig, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	switch transport {
	case "http":
		tr := &http.Transport{TLSClientConfig: tlsConfig}
		f.http = &http.Client{Transport: tr, Timeout: 3 * time.Second}
		t.Cleanup(tr.CloseIdleConnections)
		f.address = "https://" + addresses["HTTP listening"]
		rejected := tlsConfig.Clone()
		rejected.Certificates = nil
		bad := &http.Transport{TLSClientConfig: rejected}
		defer bad.CloseIdleConnections()
		if r, err := (&http.Client{Transport: bad, Timeout: time.Second}).Post(f.address+"/v1/xray/tools/instances/a/credentials/uuids", "application/json", strings.NewReader(`{}`)); err == nil {
			r.Body.Close()
			t.Fatal("HTTP accepted unverified controller")
		}
	case "grpc":
		f.grpc, err = grpc.NewClient(addresses["gRPC listening"], grpc.WithTransportCredentials(grpccredentials.NewTLS(tlsConfig)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.grpc.Close() })
		rejected := tlsConfig.Clone()
		rejected.Certificates = nil
		bad, err := grpc.NewClient(addresses["gRPC listening"], grpc.WithTransportCredentials(grpccredentials.NewTLS(rejected)))
		if err != nil {
			t.Fatal(err)
		}
		defer bad.Close()
		probe, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if _, err := credentialsv1.NewCredentialsServiceClient(bad).GenerateUUID(probe, &credentialsv1.GenerateUUIDRequest{InstanceId: "a"}); err == nil {
			t.Fatal("gRPC accepted unverified controller")
		}
	case "nats":
		f.nats, err = nats.Connect(broker, nats.Secure(tlsConfig), nats.TLSHandshakeFirst(), nats.NoReconnect())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(f.nats.Close)
	}
	return f
}

type wireArtifact struct {
	Metadata struct {
		InstanceID  string    `json:"instance_id"`
		Tool        string    `json:"tool"`
		CompletedAt time.Time `json:"completed_at"`
		DurationNS  string    `json:"duration_ns"`
	} `json:"metadata"`
	UUID        string                                  `json:"uuid"`
	PrivateKey  string                                  `json:"private_key"`
	PublicKey   string                                  `json:"public_key"`
	Hash32      string                                  `json:"hash32"`
	Seed        string                                  `json:"seed"`
	Verify      string                                  `json:"verify"`
	Client      string                                  `json:"client"`
	Certificate string                                  `json:"certificate_pem"`
	PrivatePEM  string                                  `json:"private_key_pem"`
	ConfigLists []string                                `json:"config_lists"`
	ServerKeys  string                                  `json:"server_keys"`
	X25519      struct{ Decryption, Encryption string } `json:"x25519"`
	MLKEM768    struct{ Decryption, Encryption string } `json:"mlkem768"`
	Hashes      []struct {
		Index  uint32
		SHA256 string `json:"sha256"`
	} `json:"hashes"`
	Error string `json:"error"`
}

func (f *boundaryFixture) invoke(t *testing.T, id, operation string, input map[string]any) wireArtifact {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var data []byte
	switch f.transport {
	case "http":
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal("fixture input encoding")
		}
		reply, err := f.http.Post(f.address+"/v1/xray/tools/instances/"+id+"/credentials/"+operation, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		data, err = io.ReadAll(reply.Body)
		reply.Body.Close()
		if err != nil {
			t.Fatal("reading credential response")
		}
		if reply.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable credential material")
		}
	case "nats":
		fields := make(map[string]any, len(input)+1)
		for key, value := range input {
			fields[key] = value
		}
		fields["instance_id"] = id
		body, err := json.Marshal(fields)
		if err != nil {
			t.Fatal("fixture input encoding")
		}
		suffix := map[string]string{"uuids": "uuid.v1.generate", "x25519-keypairs": "x25519.v1.generate", "wireguard-keypairs": "wireguard.v1.generate", "mldsa65-keypairs": "mldsa65.v1.generate", "mlkem768-keypairs": "mlkem768.v1.generate", "vless-encryption-pairs": "vless.v1.generate", "tls-certificates": "tls.certificates.v1.generate", "tls-certificate-hashes": "tls.hashes.v1.get", "ech-keysets": "ech.v1.generate"}[operation]
		reply, err := f.nats.RequestWithContext(ctx, "fixture.credentials.xray.tools.credentials."+suffix, body)
		if err != nil {
			t.Fatal(err)
		}
		data = reply.Data
	case "grpc":
		client := credentialsv1.NewCredentialsServiceClient(f.grpc)
		get := func(key string) *string {
			if value, ok := input[key]; ok {
				return ptr(value.(string))
			}
			return nil
		}
		var reply proto.Message
		var err error
		switch operation {
		case "uuids":
			reply, err = client.GenerateUUID(ctx, &credentialsv1.GenerateUUIDRequest{InstanceId: id, Name: get("name")})
		case "x25519-keypairs":
			encoding := credentialsv1.KeyEncoding_KEY_ENCODING_UNSPECIFIED
			if value := get("encoding"); value != nil {
				if *value == "base64" {
					encoding = credentialsv1.KeyEncoding_KEY_ENCODING_BASE64
				} else if *value != "base64url" {
					encoding = credentialsv1.KeyEncoding(999)
				}
			}
			reply, err = client.GenerateX25519(ctx, &credentialsv1.GenerateX25519Request{InstanceId: id, PrivateKey: get("private_key"), Encoding: encoding})
		case "wireguard-keypairs":
			reply, err = client.GenerateWireGuard(ctx, &credentialsv1.GenerateWireGuardRequest{InstanceId: id, PrivateKey: get("private_key")})
		case "mldsa65-keypairs":
			reply, err = client.GenerateMLDSA65(ctx, &credentialsv1.GenerateMLDSA65Request{InstanceId: id, Seed: get("seed")})
		case "mlkem768-keypairs":
			reply, err = client.GenerateMLKEM768(ctx, &credentialsv1.GenerateMLKEM768Request{InstanceId: id, Seed: get("seed")})
		case "vless-encryption-pairs":
			reply, err = client.GenerateVLESSEncryption(ctx, &credentialsv1.GenerateVLESSEncryptionRequest{InstanceId: id})
		case "tls-certificates":
			r := &credentialsv1.GenerateCertificateRequest{InstanceId: id, CommonName: get("common_name"), Organization: get("organization")}
			if value, ok := input["authority"]; ok {
				r.Authority = value.(bool)
			}
			if value, ok := input["domains"]; ok {
				r.Domains = value.([]string)
			}
			if value := get("valid_for_ns"); value != nil {
				duration, e := strconv.ParseInt(*value, 10, 64)
				if e != nil {
					t.Fatal("fixture validity")
				}
				r.ValidForNs = &duration
			}
			reply, err = client.GenerateCertificate(ctx, r)
		case "tls-certificate-hashes":
			raw, _ := base64.StdEncoding.DecodeString(*get("certificate"))
			reply, err = client.GetCertificateHashes(ctx, &credentialsv1.GetCertificateHashesRequest{InstanceId: id, Certificate: raw})
		case "ech-keysets":
			format := credentialsv1.ECHFormat_ECH_FORMAT_UNSPECIFIED
			if value := get("format"); value != nil {
				if *value == "pem" {
					format = credentialsv1.ECHFormat_ECH_FORMAT_PEM
				} else if *value != "base64" {
					format = credentialsv1.ECHFormat(999)
				}
			}
			reply, err = client.GenerateECH(ctx, &credentialsv1.GenerateECHRequest{InstanceId: id, ServerName: get("server_name"), ServerKeys: get("server_keys"), Format: format})
		default:
			t.Fatal("unknown fixture operation")
		}
		if err != nil {
			return wireArtifact{Error: status.Convert(err).Message()}
		}
		data, err = (protojson.MarshalOptions{UseProtoNames: true}).Marshal(reply)
		if err != nil {
			t.Fatal("fixture protobuf response encoding")
		}
	}
	var artifact wireArtifact
	if json.Unmarshal(data, &artifact) != nil {
		t.Fatal("credential response shape")
	}
	return artifact
}
func (f *boundaryFixture) diagnosticReport(t *testing.T) []byte {
	t.Helper()
	switch f.transport {
	case "http":
		r, err := f.http.Get(f.address + "/v1/xray/tools/instances/a/diagnostics")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		data, err := io.ReadAll(r.Body)
		if err != nil || r.StatusCode != 200 {
			t.Fatal("diagnostic request failed")
		}
		return data
	case "grpc":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		report, err := diagnosticsv1.NewDiagnosticsServiceClient(f.grpc).GetInstanceDiagnostics(ctx, &diagnosticsv1.GetInstanceDiagnosticsRequest{InstanceId: "a"})
		if err != nil {
			t.Fatal(err)
		}
		data, err := protojson.Marshal(report)
		if err != nil {
			t.Fatal("diagnostic response encoding")
		}
		return data
	case "nats":
		reply, err := f.nats.Request("fixture.credentials.xray.tools.diagnostics.v1.get", []byte(`{"instance_id":"a"}`), 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return reply.Data
	}
	t.Fatal("unknown transport")
	return nil
}
