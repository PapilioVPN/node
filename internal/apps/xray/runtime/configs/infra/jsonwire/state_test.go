package jsonwire

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

func TestStateEnvelopeRequiresUnambiguousExactRevision(t *testing.T) {
	for _, data := range []string{`null`, `[]`, `{"expected_revision":null}`, `{"expected_revision":0}`, `{"expected_revision":"01"}`, `{"expected_revision":"+1"}`, `{"expected_revision":"18446744073709551616"}`, `{"expected_revision":"1","expected_revision":"2"}`, `{"instance_id":null}`, `{"sources":null}`, `{"unknown":true}`, `{} {}`} {
		if _, err := DecodeState([]byte(data)); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatalf("ambiguous state input accepted %s: %v", data, err)
		}
	}
	result, err := DecodeState([]byte(`{"instance_id":"a","expected_revision":"18446744073709551615","sources":[{"name":"empty.pb","content":""}]}`))
	if err != nil || result.ExpectedRevision == nil || *result.ExpectedRevision != math.MaxUint64 || !result.HasSources || len(result.Sources) != 1 || result.Sources[0].Name != "empty.pb" {
		t.Fatalf("presence/precision lost %+v %v", result, err)
	}
	if _, err := DecodeState(make([]byte, (2<<20)+1)); !errors.Is(err, domain.ErrTooLarge) {
		t.Fatal("state envelope byte bound ignored")
	}
	many := strings.Repeat(`{"name":"a.json","content":"e30="},`, 32) + `{"name":"last.json","content":"e30="}`
	if _, err := DecodeState([]byte(`{"sources":[` + many + `]}`)); !errors.Is(err, domain.ErrTooLarge) {
		t.Fatal("source collection not bounded during decode")
	}
}
func TestChangeResponsePreservesLargeExecutionRevisionWithoutPrivateCauses(t *testing.T) {
	revision := uint64(math.MaxUint64)
	change := domain.Change{Persistence: domain.Persistence{Published: true, Metadata: domain.DesiredMetadata{Revision: revision}}, Execution: &instances.Control{Effect: instances.Partial, Status: &instances.Status{ID: "a", Execution: instances.Execution{Generation: revision, ConfigurationRevision: &revision, Failure: errors.New("private-credential"), CleanupFailure: errors.New("private-path")}}}}
	data, err := EncodeChange("a", change, "unavailable")
	if err != nil || strings.Contains(string(data), "private") || !strings.Contains(string(data), `"configuration_revision":"18446744073709551615"`) || !strings.Contains(string(data), `"generation":"18446744073709551615"`) || !strings.Contains(string(data), `"configuration_cleanup_failed":true`) {
		t.Fatalf("lossy or secret response %s: %v", data, err)
	}
}
func FuzzStateEnvelope(f *testing.F) {
	for _, seed := range []string{`{}`, `{"expected_revision":"0","sources":[{"name":"empty.pb","content":""}]}`, `{"instance_id":"a"}`, `{"expected_revision":null}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		result, err := DecodeState(data)
		if err == nil && len(result.Sources) > domain.MaxSources {
			t.Fatal("state source bound exceeded")
		}
	})
}
