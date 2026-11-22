//go:build integration && linux

package integration_test

import (
	"context"
	"crypto"
	"crypto/mldsa"
	"crypto/mlkem"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

func TestNativeKeysSeedsCertificatesAndECH(t *testing.T) {
	s := nativeService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, name := range []*string{nil, ptr("papilio"), ptr(" شناسه ")} {
		first, err := s.UUID(ctx, "a", domain.UUIDInput{Name: name})
		if err != nil {
			t.Fatal("native UUID", err)
		}
		if len(first.UUID) != 36 || first.CompletedAt.IsZero() {
			t.Fatal("UUID output metadata")
		}
		second, err := s.UUID(ctx, "a", domain.UUIDInput{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		if name == nil && first.UUID == second.UUID || name != nil && first.UUID != second.UUID {
			t.Fatal("UUID randomness/determinism")
		}
	}
	for _, encoding := range []domain.Encoding{domain.RawURL, domain.Standard} {
		first, err := s.X25519(ctx, "a", domain.X25519Input{Encoding: encoding})
		if err != nil {
			t.Fatal("native X25519", err)
		}
		second, err := s.X25519(ctx, "a", domain.X25519Input{Encoding: encoding, PrivateKey: &first.PrivateKey})
		if err != nil {
			t.Fatal(err)
		}
		if first.PrivateKey != second.PrivateKey || first.PublicKey != second.PublicKey || first.Hash32 != second.Hash32 {
			t.Fatal("X25519 restore")
		}
	}
	wg, err := s.WireGuard(ctx, "a", domain.WireGuardInput{})
	if err != nil {
		t.Fatal("native WireGuard", err)
	}
	restored, err := s.WireGuard(ctx, "a", domain.WireGuardInput{PrivateKey: &wg.PrivateKey})
	if err != nil || restored.PublicKey != wg.PublicKey {
		t.Fatal("WireGuard restore", err)
	}
	dsa, err := s.MLDSA65(ctx, "a", domain.SeedInput{})
	if err != nil {
		t.Fatal("native ML-DSA", err)
	}
	dsaSeed, _ := base64.RawURLEncoding.DecodeString(dsa.Seed)
	signing, err := mldsa.NewPrivateKey(mldsa.MLDSA65(), dsaSeed)
	if err != nil {
		t.Fatal("ML-DSA seed invalid")
	}
	signature, err := signing.Sign(nil, []byte("credential-proof"), crypto.Hash(0))
	if err != nil {
		t.Fatal("ML-DSA signing failed")
	}
	verifyBytes, _ := base64.RawURLEncoding.DecodeString(dsa.Verify)
	verify, err := mldsa.NewPublicKey(mldsa.MLDSA65(), verifyBytes)
	if err != nil || mldsa.Verify(verify, []byte("credential-proof"), signature, nil) != nil {
		t.Fatal("ML-DSA verification failed")
	}
	dsa2, err := s.MLDSA65(ctx, "a", domain.SeedInput{Seed: &dsa.Seed})
	if err != nil || dsa.Verify != dsa2.Verify {
		t.Fatal("ML-DSA restore", err)
	}
	kem, err := s.MLKEM768(ctx, "a", domain.SeedInput{})
	if err != nil {
		t.Fatal("native ML-KEM", err)
	}
	kemSeed, _ := base64.RawURLEncoding.DecodeString(kem.Seed)
	private, err := mlkem.NewDecapsulationKey768(kemSeed)
	if err != nil {
		t.Fatal("ML-KEM seed invalid")
	}
	publicBytes, _ := base64.RawURLEncoding.DecodeString(kem.Client)
	public, err := mlkem.NewEncapsulationKey768(publicBytes)
	if err != nil {
		t.Fatal("ML-KEM client invalid")
	}
	shared, ciphertext := public.Encapsulate()
	decapsulated, err := private.Decapsulate(ciphertext)
	if err != nil || string(shared) != string(decapsulated) {
		t.Fatal("ML-KEM key agreement failed")
	}
	kem2, err := s.MLKEM768(ctx, "a", domain.SeedInput{Seed: &kem.Seed})
	if err != nil || kem.Client != kem2.Client || kem.Hash32 != kem2.Hash32 {
		t.Fatal("ML-KEM restore", err)
	}
	if _, err := s.VLESSEncryption(ctx, "a"); err != nil {
		t.Fatal("native VLESS encryption", err)
	}
	for _, authority := range []bool{false, true} {
		certificate, err := s.Certificate(ctx, "a", domain.CertificateInput{Domains: []string{"private.example.test", "*.example.test"}, CommonName: ptr("fixture"), Organization: ptr("Papilio"), Authority: authority, ValidFor: ptr(24 * time.Hour)})
		if err != nil {
			t.Fatal("native certificate", err)
		}
		pair, err := tls.X509KeyPair([]byte(certificate.CertificatePEM), []byte(certificate.PrivateKeyPEM))
		if err != nil {
			t.Fatal("native certificate key mismatch")
		}
		cert, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil || cert.IsCA != authority || cert.VerifyHostname("private.example.test") != nil {
			t.Fatal("native certificate attributes")
		}
		for _, raw := range [][]byte{[]byte(certificate.CertificatePEM), cert.Raw} {
			hash, err := s.CertificateHashes(ctx, "a", domain.CertificateHashInput{Certificate: raw})
			if err != nil || len(hash.Hashes) != 1 || len(hash.Hashes[0].SHA256) != 64 {
				t.Fatal("native certificate hash", err)
			}
		}
	}
	for _, format := range []domain.ECHFormat{domain.Base64, domain.PEM} {
		ech, err := s.ECH(ctx, "a", domain.ECHInput{ServerName: ptr("public.example.test"), Format: format})
		if err != nil {
			t.Fatal("native ECH", err)
		}
		if len(ech.ConfigLists) != 1 {
			t.Fatal("ECH list count")
		}
		keys := ech.ServerKeys
		if format == domain.PEM {
			block, _ := pem.Decode([]byte(keys))
			if block == nil {
				t.Fatal("ECH key PEM")
			}
			keys = base64.StdEncoding.EncodeToString(block.Bytes)
		}
		again, err := s.ECH(ctx, "a", domain.ECHInput{ServerKeys: &keys, Format: format})
		if err != nil || again.ConfigLists[0] != ech.ConfigLists[0] || again.ServerKeys != ech.ServerKeys {
			t.Fatal("ECH restore", err)
		}
	}
}

func TestNativeECHArtifactsCarryEncryptedTLS(t *testing.T) {
	s := nativeService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	certificate, err := s.Certificate(ctx, "a", domain.CertificateInput{Domains: []string{"private.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []domain.ECHFormat{domain.Base64, domain.PEM} {
		ech, err := s.ECH(ctx, "a", domain.ECHInput{ServerName: ptr("public.example.test"), Format: format})
		if err != nil {
			t.Fatal(err)
		}
		echHandshake(t, certificate, ech)
	}
	// The pinned CLI can restore several key sets but concatenates individual
	// lists. Our response splits those lists, preserving native config bytes.
	a, err := s.ECH(ctx, "a", domain.ECHInput{ServerName: ptr("public.example.test")})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.ECH(ctx, "a", domain.ECHInput{ServerName: ptr("public.example.test")})
	if err != nil {
		t.Fatal(err)
	}
	rawA := echBytes(t, a.ServerKeys, a.Format)
	rawB := echBytes(t, b.ServerKeys, b.Format)
	// Native generation always uses config ID zero. Give the second test-owned
	// config a distinct ID before requesting native restoration of both keys.
	keyLength := int(binary.BigEndian.Uint16(rawB))
	rawB[2+keyLength+2+4] = 1
	combined := base64.StdEncoding.EncodeToString(append(rawA, rawB...))
	restored, err := s.ECH(ctx, "a", domain.ECHInput{ServerKeys: &combined})
	if err != nil || len(restored.ConfigLists) != 2 {
		t.Fatal("native multi-key restore failed", err)
	}
	echHandshake(t, certificate, restored)
}
