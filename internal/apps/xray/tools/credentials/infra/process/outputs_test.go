package process

import (
	"encoding/base64"
	"testing"
)

func TestRejectUnexpectedOutputWithoutReturningMaterial(t *testing.T) {
	for _, raw := range []string{"", "PrivateKey: secret\n", "Seed: private\nVerify: public\nextra\n"} {
		if values, err := fields([]byte(raw), "Seed", "Verify"); err == nil || values != nil {
			t.Fatal("malformed output accepted")
		}
	}
	for _, value := range []string{"", base64.RawURLEncoding.EncodeToString(make([]byte, 31)), "not-base64"} {
		if _, err := decode(value, base64.RawURLEncoding, 32); err == nil {
			t.Fatal("malformed key accepted")
		}
	}
	if _, err := echLists([]byte{0, 32, 1}); err == nil {
		t.Fatal("truncated ECH keys accepted")
	}
}
func FuzzECHKeyFraming(f *testing.F) {
	f.Add(validECHKeys())
	f.Add([]byte{})
	f.Add([]byte{0, 32, 1})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 32768 {
			return
		}
		echLists(data)
	})
}

func TestECHFramingPreservesPublicNameAndRejectsKeyMismatch(t *testing.T) {
	raw := validECHKeys()
	parsed, err := echLists(raw)
	if err != nil || len(parsed) != 1 || parsed[0].name != "example.test" {
		t.Fatal("valid ECH fixture did not preserve name")
	}
	raw[3] ^= 1
	if _, err := echLists(raw); err == nil {
		t.Fatal("mismatched private key accepted")
	}
}
