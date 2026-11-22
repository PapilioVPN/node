package validators

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

func TestRejectEmptyOptionalInputsAndBounds(t *testing.T) {
	empty := ""
	if UUID(domain.UUIDInput{Name: &empty}) == nil {
		t.Fatal("empty name became random UUID")
	}
	if _, err := X25519(domain.X25519Input{PrivateKey: &empty}); err == nil {
		t.Fatal("empty key became random")
	}
	if Seed(domain.SeedInput{Seed: &empty}, 32) == nil {
		t.Fatal("empty seed became random")
	}
	negative := -time.Second
	if Certificate(domain.CertificateInput{ValidFor: &negative}) == nil {
		t.Fatal("negative validity")
	}
	seed := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if Seed(domain.SeedInput{Seed: &seed}, 32) != nil {
		t.Fatal("valid seed rejected")
	}
}

func TestModeSizesCanonicalEncodingsAndCertificateOptions(t *testing.T) {
	for _, n := range []int{1, 30} {
		value := strings.Repeat("x", n)
		if UUID(domain.UUIDInput{Name: &value}) != nil {
			t.Fatal("valid UUIDv5 input size")
		}
	}
	long := strings.Repeat("x", 31)
	if UUID(domain.UUIDInput{Name: &long}) == nil {
		t.Fatal("UUIDv5 native limit")
	}
	raw := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	standard := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if _, err := X25519(domain.X25519Input{PrivateKey: &raw, Encoding: domain.RawURL}); err != nil {
		t.Fatal(err)
	}
	if _, err := X25519(domain.X25519Input{PrivateKey: &standard, Encoding: domain.Standard}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{raw + "=", raw + "\n", standard, "invalid"} {
		if _, err := X25519(domain.X25519Input{PrivateKey: &value, Encoding: domain.RawURL}); err == nil {
			t.Fatal("noncanonical private input")
		}
	}
	tooLong := domain.MaxValidity + time.Nanosecond
	if Certificate(domain.CertificateInput{ValidFor: &tooLong}) == nil {
		t.Fatal("validity bound")
	}
	if Certificate(domain.CertificateInput{Domains: make([]string, domain.MaxDomains+1)}) == nil {
		t.Fatal("SAN count bound")
	}
	key := base64.StdEncoding.EncodeToString([]byte("placeholder"))
	name := "example.test"
	if _, err := ECH(domain.ECHInput{ServerKeys: &key, ServerName: &name}); err == nil {
		t.Fatal("restoration accepted ignored server name")
	}
	if _, err := ECH(domain.ECHInput{Format: "unsupported"}); err == nil {
		t.Fatal("unknown ECH format")
	}
}
