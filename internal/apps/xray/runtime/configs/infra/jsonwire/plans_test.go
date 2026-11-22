package jsonwire

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

func TestReviewedIntentsRejectAmbiguousFieldsAndRevisions(t *testing.T) {
	for _, data := range []string{`null`, `[]`, `{"plan_id":null}`, `{"plan_id":"a","plan_id":"b"}`, `{"unknown":"a"}`, `{"plan_id":123}`, `{} {}`} {
		if _, err := DecodeIntent([]byte(data), "plan_id"); err == nil {
			t.Fatal("ambiguous intent accepted", data)
		}
	}
	for _, revision := range []string{"", "00", "+1", "-1", "18446744073709551616"} {
		if _, err := Revision(map[string]string{"expected_revision": revision}); err == nil {
			t.Fatal(revision)
		}
	}
	value, err := Revision(map[string]string{"expected_revision": "18446744073709551615"})
	if err != nil || value != math.MaxUint64 {
		t.Fatal(value, err)
	}
}
func TestApplicationEnvelopePreservesExactVersionsAndContainsNoSources(t *testing.T) {
	data, err := EncodeApplication(domain.Application{ID: strings.Repeat("a", 32), Revision: math.MaxUint64, ExpectedRevision: math.MaxUint64 - 1, Generation: math.MaxUint64, Outcome: "unknown", EngineEffect: "unknown", RequiresRecovery: true})
	if err != nil || !bytes.Contains(data, []byte(`"revision":"18446744073709551615"`)) || bytes.Contains(data, []byte(`"sources"`)) || bytes.Contains(data, []byte(`"content"`)) {
		t.Fatal(string(data), err)
	}
}
func FuzzReviewedConfigurationRequests(f *testing.F) {
	f.Add([]byte(`{"instance_id":"a","plan_id":"01234567890123456789012345678901"}`))
	f.Add([]byte(`{"expected_revision":"0","sources":[{"name":"a.pb","content":""}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		DecodeIntent(data, "instance_id", "application_id", "plan_id", "expected_revision", "recovery_id")
		DecodeState(data)
	})
}
