//go:build integration && linux

package integration_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/infra/process"
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
