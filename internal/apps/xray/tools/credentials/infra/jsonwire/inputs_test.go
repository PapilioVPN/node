package jsonwire

import (
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

func TestStrictInputPresenceAndSecretsAreNeverErrors(t *testing.T) {
	for _, input := range []string{`{}`, `{"instance_id":null}`, `{"instance_id":"a","name":null}`, `{"instance_id":"a","name":"first","name":"private-fixture"}`, `{"instance_id":"a","command":"run"}`, `{"instance_id":"a","Name":"private-fixture"}`, `{"instance_id":"a"} {}`} {
		if _, _, err := DecodeUUID([]byte(input), ""); err == nil || strings.Contains(err.Error(), "private-fixture") {
			t.Fatal("strict decode or error privacy")
		}
	}
	if _, _, err := DecodeUUID([]byte(`{"instance_id":"b"}`), "a"); err == nil {
		t.Fatal("body overrode path identity")
	}
	if id, r, err := DecodeUUID([]byte(`{"name":""}`), "a"); err != nil || id != "a" || r.Name == nil || *r.Name != "" {
		t.Fatal("optional empty presence lost")
	}
	for _, input := range []string{`{"domains":null}`, `{"domains":[null]}`, `{"valid_for_ns":1}`, `{"valid_for_ns":"01"}`, `{"authority":null}`, `{"file":"/tmp/private"}`} {
		if _, _, err := DecodeCertificate([]byte(input), "a"); err == nil {
			t.Fatal("invalid certificate shape")
		}
	}
	for _, decode := range []func([]byte, string) (string, error){
		func(b []byte, id string) (string, error) { id, _, err := DecodeX25519(b, id); return id, err },
		func(b []byte, id string) (string, error) { id, _, err := DecodeCertificate(b, id); return id, err },
		func(b []byte, id string) (string, error) { id, _, err := DecodeECH(b, id); return id, err },
	} {
		if _, err := decode([]byte(strings.Repeat("x", domain.MaxRequestBytes+1)), "a"); err == nil {
			t.Fatal("request bytes unbounded")
		}
	}
}
func FuzzCredentialEnvelopes(f *testing.F) {
	f.Add([]byte(`{"instance_id":"a","seed":""}`))
	f.Add([]byte(`{"domains":["example.test"],"valid_for_ns":"1"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		DecodeUUID(data, "")
		DecodeX25519(data, "a")
		DecodeWireGuard(data, "a")
		DecodeSeed(data, "")
		DecodeEmpty(data, "")
		DecodeCertificate(data, "a")
		DecodeCertificateHashes(data, "a")
		DecodeECH(data, "a")
	})
}
