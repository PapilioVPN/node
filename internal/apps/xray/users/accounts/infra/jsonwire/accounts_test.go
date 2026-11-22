package jsonwire_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/jsonwire"
)

func TestCountPreservesIntegersAndAbsenceIsExplicit(t *testing.T) {
	data, err := jsonwire.EncodeCount(domain.Count{Observation: domain.Observation{Generation: 9007199254740993}, Count: 9223372036854775807})
	if err != nil || !strings.Contains(string(data), `"count":"9223372036854775807"`) || !strings.Contains(string(data), `"generation":"9007199254740993"`) {
		t.Fatal(string(data), err)
	}
	data, err = jsonwire.EncodeAccount(domain.Account{RequestedEmail: "missing@example.test"})
	if err != nil || !strings.Contains(string(data), `"user":null`) {
		t.Fatal(string(data), err)
	}
}
func FuzzSelection(f *testing.F) {
	for _, seed := range []string{`{}`, `{"instance_id":"a","inbound_tag":"b"}`, `{"email":"a","inbound_tag":"b","instance_id":"c"}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		if _, err := jsonwire.DecodeSelection([]byte(data)); err == nil && !json.Valid([]byte(data)) {
			t.Fatal("invalid JSON accepted")
		}
	})
}
