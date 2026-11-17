// Package testutil contains technical fixtures shared by transport test suites.
package testutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/shared/mtls"
)

type PKI struct {
	Server mtls.Files
	Client mtls.Files
}

// NewPKI creates isolated server/client identities with role-specific usages.
// All private keys exist only in the calling test's temporary directory.
func NewPKI(t testing.TB) PKI {
	t.Helper()
	dir := t.TempDir()
	now := time.Now()
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture-ca"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(dir, "ca.crt")
	writePEM(t, caPath, "CERTIFICATE", caDER)
	issue := func(name string, serial int64, usage x509.ExtKeyUsage) mtls.Files {
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		certificate := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
			DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		}
		der, err := x509.CreateCertificate(rand.Reader, certificate, ca, public, caPrivate)
		if err != nil {
			t.Fatal(err)
		}
		key, err := x509.MarshalPKCS8PrivateKey(private)
		if err != nil {
			t.Fatal(err)
		}
		files := mtls.Files{
			Certificate: filepath.Join(dir, name+".crt"),
			PrivateKey:  filepath.Join(dir, name+".key"), CA: caPath,
		}
		writePEM(t, files.Certificate, "CERTIFICATE", der)
		writePEM(t, files.PrivateKey, "PRIVATE KEY", key)
		return files
	}
	return PKI{
		Server: issue("server", 2, x509.ExtKeyUsageServerAuth),
		Client: issue("client", 3, x509.ExtKeyUsageClientAuth),
	}
}

func writePEM(t testing.TB, path, kind string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data}), 0o600); err != nil {
		t.Fatal(err)
	}
}
