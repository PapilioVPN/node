package jsonwire

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
)

func TestPrivateCausesAreExcludedAndIntegerWidthsAreExact(t *testing.T) {
	private := errors.New("password-private private-key /private/config.json")
	at := time.Now().UTC()
	e := domain.Evidence{State: "unavailable", Reason: "source_unavailable", Source: "xray.native_api", Cause: private, ObservedAt: &at}
	revision := uint64(math.MaxUint64)
	r := domain.Report{InstanceID: "edge", Execution: domain.Execution{Evidence: e, Generation: revision, AppliedRevision: &revision, FailureReason: "execution_failed"}, Binary: domain.Binary{Evidence: e}, Configuration: domain.Configuration{Evidence: e, DesiredRevision: &revision}, Native: domain.Native{Evidence: e, Runtime: e, Discovery: e}}
	data, err := Encode(r)
	if err != nil || strings.Contains(string(data), "private") || !strings.Contains(string(data), `"generation":"18446744073709551615"`) || !strings.Contains(string(data), `"desired_revision":"18446744073709551615"`) || len(data) > domain.MaxReportBytes {
		t.Fatal(string(data), err)
	}
	r.Native.Capabilities = make([]domain.Capability, 1000)
	for i := range r.Native.Capabilities {
		r.Native.Capabilities[i].Name = strings.Repeat("x", 100)
	}
	if data, err := Encode(r); !errors.Is(err, domain.ErrCapacity) || data != nil {
		t.Fatal(len(data), err)
	}
}
func TestStrictInputAndPresence(t *testing.T) {
	for _, input := range []string{`{}`, `null`, `{"instance_id":null}`, `{"instance_id":"edge","instance_id":"other"}`, `{"instance_id":"edge","key":"private"}`, `{"instance_id":"edge","max_age_seconds":"1"}`, `{"instance_id":"edge"} {}`, strings.Repeat(" ", 1025)} {
		if _, err := Decode([]byte(input)); err == nil {
			t.Fatal(input)
		}
	}
	r, err := Decode([]byte(`{"instance_id":"edge","max_age_seconds":1}`))
	if err != nil || r.InstanceID != "edge" || r.MaxAgeSeconds != 1 {
		t.Fatal(r, err)
	}
}
func FuzzDecode(f *testing.F) {
	f.Add(`{"instance_id":"edge"}`)
	f.Add("null")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 2048 {
			return
		}
		_, _ = Decode([]byte(s))
	})
}
