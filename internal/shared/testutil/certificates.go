// Package testutil supplies isolated certificate fixtures for native runtime tests.
package testutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/PapilioVPN/node/internal/settings"
)

type CertificatePair struct{ Certificate, Key string }

type Certificates struct {
	Clients           map[string]CertificatePair
	Directory         string
	CA                string
	ServerCertificate string
	ServerKey         string
	ClientCertificate string
	ClientKey         string
}

func NewCertificates(t testing.TB, clientNames ...string) Certificates {
	t.Helper()
	directory := t.TempDir()
	newKey := func() *ecdsa.PrivateKey {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	caKey := newKey()
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated-test-ca"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	write := func(name, kind string, data []byte) string {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data}), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(name string, serial int64, usage x509.ExtKeyUsage) (string, string) {
		key := newKey()
		certificate := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
			NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		if usage == x509.ExtKeyUsageServerAuth {
			certificate.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
			certificate.DNSNames = []string{"localhost"}
		}
		der, err := x509.CreateCertificate(rand.Reader, certificate, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		private, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return write(name+".pem", "CERTIFICATE", der), write(name+"-key.pem", "PRIVATE KEY", private)
	}
	server, serverKey := issue("server", 2, x509.ExtKeyUsageServerAuth)
	client, clientKey := issue("client", 3, x509.ExtKeyUsageClientAuth)
	clients := make(map[string]CertificatePair, len(clientNames))
	for i, name := range clientNames {
		certificate, key := issue(name, int64(i+4), x509.ExtKeyUsageClientAuth)
		clients[name] = CertificatePair{certificate, key}
	}
	return Certificates{Clients: clients, Directory: directory, CA: write("ca.pem", "CERTIFICATE", caDER), ServerCertificate: server, ServerKey: serverKey, ClientCertificate: client, ClientKey: clientKey}
}

func (c Certificates) ClientTLS(t testing.TB, identity ...string) *tls.Config {
	t.Helper()
	ca, err := os.ReadFile(c.CA)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid test CA")
	}
	certificate, key := c.ClientCertificate, c.ClientKey
	if len(identity) > 0 {
		pair, ok := c.Clients[identity[0]]
		if !ok {
			t.Fatal("unknown test client identity")
		}
		certificate, key = pair.Certificate, pair.Key
	}
	client, err := tls.LoadX509KeyPair(certificate, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{client}, ServerName: "localhost"}
}

func (c Certificates) Config() settings.Config {
	return settings.Config{
		Node:    settings.Node{ID: "test-node"},
		Runtime: settings.Runtime{StartupTimeout: 3 * time.Second, ShutdownTimeout: 3 * time.Second},
		Communications: settings.Communications{GRPC: settings.GRPC{
			Enabled: true, Listen: "127.0.0.1:0", RequestTimeout: time.Second, ConnectionTimeout: time.Second,
			MaxReceiveBytes: 1024, MaxConcurrentStreams: 16,
			TLS: settings.TLS{CertificateFile: c.ServerCertificate, PrivateKeyFile: c.ServerKey, ClientCAFile: c.CA},
		}},
	}
}

// GRPCClient opens an authenticated management connection owned by this test.
func (c Certificates) GRPCClient(t testing.TB, address string) *grpc.ClientConn {
	t.Helper()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(c.ClientTLS(t))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	return connection
}
