//go:build integration && linux

package integration_test

import (
	"crypto/ecdh"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"os"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNativeCredentialTransportParityAndPrivacy(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	brokerURL := strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
	var namedUUID string
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			var f *boundaryFixture
			var secrets []string
			t.Cleanup(func() {
				if f == nil {
					return
				}
				logs := f.logs.text()
				for _, secret := range secrets {
					if len(secret) > 0 && strings.Contains(logs, secret) {
						t.Error("credential material appeared in management logs")
					}
				}
			})
			f = boundary(t, pki, brokerURL, transport)
			for _, op := range []string{"uuids", "x25519-keypairs", "wireguard-keypairs", "mldsa65-keypairs", "mlkem768-keypairs", "vless-encryption-pairs", "tls-certificates", "ech-keysets"} {
				r := f.invoke(t, "a", op, map[string]any{})
				if r.Error != "" || r.Metadata.InstanceID != "a" || !strings.HasPrefix(r.Metadata.Tool, "xray.") || r.Metadata.CompletedAt.IsZero() || r.Metadata.DurationNS == "" {
					t.Fatal("native artifact or metadata", op, r.Error)
				}
				secrets = append(secrets, r.PrivateKey, r.Seed, r.X25519.Decryption, r.MLKEM768.Decryption)
				switch op {
				case "uuids":
					if len(r.UUID) != 36 {
						t.Fatal("UUID field lost")
					}
				case "x25519-keypairs", "wireguard-keypairs":
					encoding := base64.RawURLEncoding
					if op == "wireguard-keypairs" {
						encoding = base64.StdEncoding
					}
					private, err := encoding.DecodeString(r.PrivateKey)
					if err != nil {
						t.Fatal("private key wire encoding")
					}
					key, err := ecdh.X25519().NewPrivateKey(private)
					if err != nil || encoding.EncodeToString(key.PublicKey().Bytes()) != r.PublicKey {
						t.Fatal("wire public key does not match")
					}
					again := f.invoke(t, "a", op, map[string]any{"private_key": r.PrivateKey})
					if again.Error != "" || again.PrivateKey != r.PrivateKey || again.PublicKey != r.PublicKey || again.Hash32 != r.Hash32 {
						t.Fatal("curve derivation transport", again.Error)
					}
				case "mldsa65-keypairs", "mlkem768-keypairs":
					again := f.invoke(t, "a", op, map[string]any{"seed": r.Seed})
					if again.Error != "" || again.Seed != r.Seed || again.Verify != r.Verify || again.Client != r.Client || again.Hash32 != r.Hash32 {
						t.Fatal("seed restoration transport", again.Error)
					}
				case "vless-encryption-pairs":
					if !strings.HasPrefix(r.X25519.Decryption, "mlkem768x25519plus.native.600s.") || r.MLKEM768.Encryption == "" {
						t.Fatal("VLESS fields lost")
					}
				case "tls-certificates":
					pair, err := tls.X509KeyPair([]byte(r.Certificate), []byte(r.PrivatePEM))
					if err != nil {
						t.Fatal("certificate key pair transport")
					}
					block, _ := pem.Decode([]byte(r.PrivatePEM))
					if block == nil {
						t.Fatal("private PEM lost")
					}
					secrets = append(secrets, base64.StdEncoding.EncodeToString(block.Bytes), strings.Split(r.PrivatePEM, "\n")[1])
					hash := f.invoke(t, "a", "tls-certificate-hashes", map[string]any{"certificate": base64.StdEncoding.EncodeToString([]byte(r.Certificate))})
					expected := sha256.Sum256(pair.Certificate[0])
					if hash.Error != "" || len(hash.Hashes) != 1 || hash.Hashes[0].SHA256 != hex.EncodeToString(expected[:]) {
						t.Fatal("certificate hash transport", hash.Error)
					}
					disabled := f.invoke(t, "b", "tls-certificate-hashes", map[string]any{"certificate": base64.StdEncoding.EncodeToString([]byte(r.Certificate))})
					if disabled.Error != "facility_not_configured" {
						t.Fatal("missing workspace misclassified", disabled.Error)
					}
				case "ech-keysets":
					if len(r.ConfigLists) != 1 || r.ServerKeys == "" {
						t.Fatal("ECH fields lost")
					}
					secrets = append(secrets, r.ServerKeys)
					again := f.invoke(t, "a", op, map[string]any{"server_keys": r.ServerKeys, "format": "pem"})
					if again.Error != "" || len(again.ConfigLists) != 1 || !strings.HasPrefix(again.ServerKeys, "-----BEGIN ECH KEYS-----") {
						t.Fatal("ECH restoration/PEM transport", again.Error)
					}
					secrets = append(secrets, strings.Split(again.ServerKeys, "\n")[1])
				}
			}
			named := f.invoke(t, "a", "uuids", map[string]any{"name": "transport-fixture"})
			if named.Error != "" {
				t.Fatal(named.Error)
			}
			if namedUUID == "" {
				namedUUID = named.UUID
			} else if named.UUID != namedUUID {
				t.Fatal("UUIDv5 differs across transports")
			}
			standard := f.invoke(t, "b", "x25519-keypairs", map[string]any{"encoding": "base64"})
			if standard.Error != "" || standard.Metadata.InstanceID != "b" || len(standard.PrivateKey) != 44 {
				t.Fatal("instance or encoding selection", standard.Error)
			}
			secrets = append(secrets, standard.PrivateKey)
			for _, test := range []struct {
				op    string
				input map[string]any
			}{
				{"uuids", map[string]any{"name": ""}},
				{"x25519-keypairs", map[string]any{"private_key": ""}},
				{"wireguard-keypairs", map[string]any{"private_key": "private-request-marker"}},
				{"mldsa65-keypairs", map[string]any{"seed": ""}},
				{"mlkem768-keypairs", map[string]any{"seed": ""}},
				{"tls-certificates", map[string]any{"domains": []string{"invalid/name"}}},
				{"tls-certificates", map[string]any{"valid_for_ns": "-1"}},
				{"tls-certificate-hashes", map[string]any{"certificate": base64.StdEncoding.EncodeToString([]byte("private-request-marker"))}},
				{"ech-keysets", map[string]any{"server_name": "bad/name"}},
				{"ech-keysets", map[string]any{"server_keys": "private-request-marker"}},
			} {
				r := f.invoke(t, "a", test.op, test.input)
				if r.Error != "invalid_argument" || r.PrivateKey != "" || r.Seed != "" || r.ServerKeys != "" || r.PrivatePEM != "" {
					t.Fatal("invalid request returned material", test.op, r.Error)
				}
			}
			for _, id := range []string{"unknown", "missing-binary"} {
				r := f.invoke(t, id, "uuids", map[string]any{})
				expected := "not_found"
				if id == "missing-binary" {
					expected = "unavailable"
				}
				if r.Error != expected || r.UUID != "" {
					t.Fatal("instance failure", id, r.Error)
				}
			}
			report := string(f.diagnosticReport(t))
			for _, secret := range append(secrets, "private-request-marker") {
				if secret != "" && (strings.Contains(report, secret) || strings.Contains(f.logs.text(), secret)) {
					t.Fatal("credential leaked into diagnostics or logs")
				}
			}
			for _, forbidden := range []string{"private_key", "server_keys", "decryption", "certificate_pem"} {
				if strings.Contains(report, forbidden) {
					t.Fatal("artifact fields appeared in shareable diagnostics")
				}
			}
			files, err := os.ReadDir(f.work)
			if err != nil || len(files) != 0 {
				t.Fatal("credential request left workspace artifacts")
			}
		})
	}
}
