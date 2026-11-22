package jsonwire

import (
	"strings"
	"testing"
)

func TestReplacementJSONRejectsAmbiguousPolicyAndPayload(t *testing.T) {
	valid := `{"tag":"a","configuration":"AQ==","default_policy":"preserve","connection_policy":"retain"}`
	if _, err := DecodePlan([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`null`, `[]`, valid + `{}`, strings.Replace(valid, `"default_policy":"preserve"`, `"default_policy":null`, 1), strings.Replace(valid, `"tag":"a"`, `"tag":"a","tag":"b"`, 1), strings.Replace(valid, `"configuration":"AQ=="`, `"configuration":"AQ==","sources":[]`, 1), strings.Replace(valid, `"tag":"a"`, `"unknown":"a"`, 1)} {
		if _, err := DecodePlan([]byte(data)); err == nil {
			t.Fatal("ambiguous plan accepted", data)
		}
	}
}
func FuzzReplacementRequests(f *testing.F) {
	f.Add([]byte(`{"instance_id":"a","plan_id":"01234567890123456789012345678901"}`))
	f.Add([]byte(`{"tag":"a","configuration":"AQ==","default_policy":"preserve","connection_policy":"retain"}`))
	f.Fuzz(func(t *testing.T, data []byte) { DecodePlan(data); DecodePlanID(data); DecodePage(data) })
}
