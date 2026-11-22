package jsonwire

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
)

func TestRequestPresenceAndStrictBounds(t *testing.T) {
	for _, data := range []string{`{}`, `null`, `[]`, `{"instance_id":"edge"}`, `{"instance_id":"edge","outbound_tag":null}`, `{"instance_id":"edge","instance_id":"other","outbound_tag":""}`, `{"instance_id":"edge","outbound_tag":"","extra":true}`, `{"instance_id":"edge","outbound_tag":""} {}`, `{"instance_id":"edge","outbound_tag":"","max_sample_age_seconds":-1}`, `{"instance_id":"edge","outbound_tag":"","max_sample_age_seconds":"1"}`} {
		if _, err := Decode([]byte(data), false); err == nil {
			t.Fatal(data)
		}
	}
	got, err := Decode([]byte(`{"instance_id":"edge","outbound_tag":"","max_sample_age_seconds":1}`), false)
	if err != nil || got.Tag == nil || *got.Tag != "" || got.Page.MaxSampleAgeSeconds != 1 {
		t.Fatal(got, err)
	}
	for _, data := range []string{`{"instance_id":"edge","outbound_tag":"x"}`, `{"instance_id":"edge","page_size":null}`, `{"instance_id":"edge","page_size":4294967296}`, `{"instance_id":"edge","page_size":1.5}`, strings.Repeat(" ", MaxRequestBytes+1)} {
		if _, err := Decode([]byte(data), true); err == nil {
			t.Fatal(data[:min(80, len(data))])
		}
	}
}
func TestJSONIntegerPresencePrivacyAndResponseBound(t *testing.T) {
	n := int64(math.MaxInt64)
	at := time.Unix(1000, 0).UTC()
	o := domain.Observation{Generation: math.MaxUint64, Status: &domain.Status{Record: domain.Record{DelayMS: math.MinInt64, HealthPing: &domain.HealthPing{All: math.MaxInt64}}, LatencyMS: &n, Freshness: "unknown"}, ObservedAt: at}
	data, err := EncodeObservation(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"generation":"18446744073709551615"`, `"delay_ms":"-9223372036854775808"`, `"latency_ms":"9223372036854775807"`, `"all":"9223372036854775807"`, `"sampled_at":null`} {
		if !strings.Contains(string(data), value) {
			t.Fatal(string(data), value)
		}
	}
	data, _ = EncodeObservation(domain.Observation{})
	if !strings.Contains(string(data), `"status":null`) {
		t.Fatal(string(data))
	}
	p := domain.Page{Statuses: make([]domain.Status, 50)}
	for i := range p.Statuses {
		p.Statuses[i].Tag = strings.Repeat("\x00", domain.MaxTagBytes)
	}
	data, err = EncodePage(p)
	if err != nil || len(data) > 2<<20 {
		t.Fatal(len(data), err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	t.Logf("maximum page with worst-case escaped tags: %d JSON bytes (<2MiB)", len(data))
}
func FuzzDecode(f *testing.F) {
	for _, s := range []string{`{"instance_id":"edge","outbound_tag":""}`, `{"instance_id":"edge","page_size":50}`, `null`, ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > MaxRequestBytes+1 {
			return
		}
		_, _ = Decode([]byte(s), false)
		_, _ = Decode([]byte(s), true)
	})
}
