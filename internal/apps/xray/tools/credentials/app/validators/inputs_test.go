package validators

import (
	"encoding/base64"
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
