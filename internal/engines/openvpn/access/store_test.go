package access

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
)

func TestReplaceRejectsForeignAndStaleLists(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	root := t.TempDir()
	ca, key, data := issuer(t, now)
	foreign, foreignKey, _ := issuer(t, now)
	store := Store{CAFile: filepath.Join(root, "ca.pem"), Path: filepath.Join(root, "crl.pem")}
	if err := os.WriteFile(store.CAFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	initial := signed(t, ca, key, now, 1)
	first, err := store.Replace(t.Context(), initial)
	if err != nil || first.Number != "1" || len(first.SHA256) != 64 {
		t.Fatalf("first = %+v, %v", first, err)
	}
	old, err := os.Stat(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string][]byte{"foreign": signed(t, foreign, foreignKey, now, 2), "stale": signed(t, ca, key, now, 0)} {
		t.Run(name, func(t *testing.T) {
			_, err := store.Replace(t.Context(), candidate)
			if !errors.Is(err, lifecycle.ErrRevocationRejected) {
				t.Fatalf("Replace = %v", err)
			}
			got, err := store.Read()
			if err != nil || got.SHA256 != first.SHA256 {
				t.Fatalf("previous revision lost = %+v, %v", got, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.Replace(ctx, signed(t, ca, key, now, 2)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Replace = %v", err)
	}
	second, err := store.Replace(t.Context(), signed(t, ca, key, now, 2))
	if err != nil || second.Number != "2" || second.SHA256 == first.SHA256 {
		t.Fatalf("second = %+v, %v", second, err)
	}
	updated, err := os.Stat(store.Path)
	if err != nil || updated.ModTime().Unix() == old.ModTime().Unix() {
		t.Fatalf("native reload timestamp did not change: %v, %v", updated, err)
	}
}

func issuer(t *testing.T, now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture issuer"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return ca, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func signed(t *testing.T, ca *x509.Certificate, key *ecdsa.PrivateKey, now time.Time, number int64) []byte {
	t.Helper()
	der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: big.NewInt(number), ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Minute),
	}, ca, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: der})
}
