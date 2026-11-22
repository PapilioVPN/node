package jsonwire

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
)

func TestStrictSelectionAndExactIntegerPresence(t *testing.T) {
	for _, input := range []string{`{}`, `null`, `[]`, `{"instance_id":"a"}`, `{"instance_id":"a","email":null}`, `{"instance_id":"a","email":"x","email":"y"}`, `{"instance_id":"a","email":"x","other":1}`, `{"instance_id":"a","email":"x"} {}`} {
		if _, err := DecodeSelection([]byte(input)); err == nil {
			t.Fatal(input)
		}
	}
	s, err := DecodeSelection([]byte(`{"email":" A>>>b ","instance_id":"a"}`))
	if err != nil || s.Email != " A>>>b " {
		t.Fatal(s, err)
	}
	max := int64(math.MaxInt64)
	data, err := EncodeStatus(domain.Status{Observation: domain.Observation{Selection: s, Generation: math.MaxUint64}, Summary: domain.Summary{Count: &max}})
	if err != nil || !strings.Contains(string(data), `"count":"9223372036854775807"`) || !strings.Contains(string(data), `"generation":"18446744073709551615"`) {
		t.Fatal(string(data), err)
	}
	data, _ = EncodeStatus(domain.Status{})
	if !strings.Contains(string(data), `"count":null`) || !strings.Contains(string(data), `"state":"untracked"`) {
		t.Fatal(string(data))
	}
	zero := int64(0)
	data, _ = EncodeStatus(domain.Status{Summary: domain.Summary{Count: &zero}})
	if !strings.Contains(string(data), `"count":"0"`) || !strings.Contains(string(data), `"state":"offline"`) {
		t.Fatal(string(data))
	}
	for _, tracked := range []bool{false, true} {
		data, err = EncodeIPs(domain.IPList{Visitors: domain.Visitors{Tracked: tracked}})
		want := `"ips":null`
		if tracked {
			want = `"ips":[]`
		}
		if err != nil || !strings.Contains(string(data), want) {
			t.Fatal(string(data), err)
		}
	}
	data, _ = EncodeIPs(domain.IPList{Visitors: domain.Visitors{Tracked: true, IPs: []domain.IP{{Address: "opaque", LastSeenUnixSeconds: math.MinInt64}}}})
	if !strings.Contains(string(data), `"last_seen_unix_seconds":"-9223372036854775808"`) {
		t.Fatal(string(data))
	}
}
func FuzzDecodeSelection(f *testing.F) {
	for _, s := range []string{`{"instance_id":"a","email":"x"}`, `{}`, `null`, `{"instance_id":"a","email":null}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := DecodeSelection(data)
		if err != nil {
			return
		}
		encoded, err := json.Marshal(struct {
			ID    string `json:"instance_id"`
			Email string `json:"email"`
		}{s.InstanceID, s.Email})
		if err != nil {
			t.Fatal(err)
		}
		again, err := DecodeSelection(encoded)
		if err != nil || s != again {
			t.Fatal(s, again, err)
		}
	})
}
