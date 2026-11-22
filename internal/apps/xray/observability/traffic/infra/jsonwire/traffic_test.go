package jsonwire

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
)

func TestStrictRequestAndExactSignedJSON(t *testing.T) {
	valid := `{"direction":"downlink","name":" A>>>B ","scope":"user","instance_id":"a"}`
	id, selection, err := DecodeRequest([]byte(valid))
	if err != nil || id != "a" || selection.Name != " A>>>B " || selection.Direction != domain.Downlink {
		t.Fatal(id, selection, err)
	}
	for _, data := range []string{`null`, `[]`, `{}`, valid + `{}`, strings.Replace(valid, `"user"`, `null`, 1), strings.Replace(valid, `"user"`, `1`, 1), strings.Replace(valid, `"scope":"user"`, `"scope":"user","scope":"user"`, 1), strings.Replace(valid, `"scope":"user"`, `"unknown":"user"`, 1), strings.Repeat(" ", MaxRequestBytes+1)} {
		if _, _, err := DecodeRequest([]byte(data)); err == nil {
			t.Fatalf("accepted %q", data[:min(128, len(data))])
		}
	}
	for _, value := range []int64{0, math.MaxInt64, math.MinInt64} {
		data, err := Encode(domain.Observation{InstanceID: "a", Generation: math.MaxUint64, Selection: selection, Bytes: value, Source: domain.Source, ObservedAt: time.Unix(0, 0).UTC()})
		var got struct {
			Bytes      int64  `json:"bytes,string"`
			Generation uint64 `json:"generation,string"`
		}
		if err != nil || json.Unmarshal(data, &got) != nil || got.Bytes != value || got.Generation != math.MaxUint64 {
			t.Fatalf("lossy JSON %s %v", data, err)
		}
	}
}

func FuzzDecodeRequest(f *testing.F) {
	f.Add([]byte(`{"instance_id":"a","scope":"user","name":"b","direction":"uplink"}`))
	f.Add([]byte(`{"scope":null}`))
	f.Fuzz(func(t *testing.T, data []byte) { DecodeRequest(data) })
}
