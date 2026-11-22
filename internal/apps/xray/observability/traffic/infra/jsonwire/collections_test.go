package jsonwire

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
)

func TestResetShapeRequiresRevisionPresenceAndCanonicalInteger(t *testing.T) {
	valid := `{"instance_id":"a","epoch":"01234567890123456789012345678901","operation_id":"op","expected_revision":"0","scope":"user","name":" A>>>B ","direction":"uplink"}`
	if id, r, err := DecodeReset([]byte(valid), domain.ResetExact); err != nil || id != "a" || r.ExpectedRevision != 0 || r.Selection.Name != " A>>>B " {
		t.Fatal(id, r, err)
	}
	for _, data := range []string{
		strings.Replace(valid, `"expected_revision":"0",`, "", 1),
		strings.Replace(valid, `"expected_revision":"0"`, `"expected_revision":0`, 1),
		strings.Replace(valid, `"expected_revision":"0"`, `"expected_revision":"00"`, 1),
		strings.Replace(valid, `"expected_revision":"0"`, `"expected_revision":"-1"`, 1),
		strings.Replace(valid, `"scope":"user"`, `"scope":"user","scope":"user"`, 1),
		strings.Replace(valid, `"name":" A>>>B "`, `"name":null`, 1), valid + `{}`, `null`, `[]`,
	} {
		if _, _, err := DecodeReset([]byte(data), domain.ResetExact); err == nil {
			t.Fatal("accepted invalid reset", data)
		}
	}
	if _, _, err := DecodeReset([]byte(valid), domain.ResetScope); err == nil {
		t.Fatal("scope accepted name/direction")
	}
}
func TestResetAndPairsKeepNilZeroAndSignedExtremes(t *testing.T) {
	for _, value := range []int64{0, math.MinInt64, math.MaxInt64} {
		at := time.Unix(0, 0).UTC()
		data, err := EncodeReset(domain.ResetReport{Revision: math.MaxUint64, Items: []domain.ResetItem{{Effect: domain.Acknowledged, PreviousBytes: &value, AcknowledgedAt: &at}, {Effect: domain.Unknown}}})
		var result struct {
			Revision uint64 `json:"revision,string"`
			Items    []struct {
				PreviousBytes  *int64     `json:"previous_bytes,string"`
				AcknowledgedAt *time.Time `json:"acknowledged_at"`
			} `json:"items"`
		}
		if err != nil || json.Unmarshal(data, &result) != nil || result.Revision != math.MaxUint64 || *result.Items[0].PreviousBytes != value || result.Items[1].PreviousBytes != nil || result.Items[0].AcknowledgedAt == nil {
			t.Fatal(string(data), err)
		}
	}
	data, _ := EncodePair(domain.Pair{Uplink: &domain.Observation{Bytes: 0}})
	if !strings.Contains(string(data), `"bytes":"0"`) || !strings.Contains(string(data), `"downlink":null`) {
		t.Fatal(string(data))
	}
}
func FuzzCollectionRequests(f *testing.F) {
	f.Add([]byte(`{"instance_id":"a","scope":"user","page_size":50}`))
	f.Add([]byte(`{"instance_id":"a","scope":"outbound","epoch":"01234567890123456789012345678901","operation_id":"op","expected_revision":"0","max_counters":1000}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		DecodePage(data)
		DecodePair(data)
		DecodeStatus(data)
		DecodeReset(data, domain.ResetExact)
		DecodeReset(data, domain.ResetPair)
		DecodeReset(data, domain.ResetScope)
	})
}
