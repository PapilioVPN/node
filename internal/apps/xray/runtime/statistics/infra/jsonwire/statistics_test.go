package jsonwire

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/domain"
)

func TestRequestIdentityIsUnambiguousAndBounded(t *testing.T) {
	if id, err := DecodeRequest([]byte(`{"instance_id":"edge-a"}`)); err != nil || id != "edge-a" {
		t.Fatal(id, err)
	}
	for _, body := range []string{`{}`, `null`, `[]`, `{"instance_id":"a","instance_id":"b"}`, `{"instance_id":"a","unknown":true}`, `{"instance_id":["a"]}`, `{"instance_id":"a"} {}`, strings.Repeat(" ", 2049)} {
		if _, err := DecodeRequest([]byte(body)); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatalf("ambiguous selection accepted: %s", body)
		}
	}
}
func TestRuntimeJSONPreservesUint64AndNativeCounterWidths(t *testing.T) {
	encoded, err := Encode(domain.Observation{InstanceID: "a", Generation: math.MaxUint64, Statistics: domain.Statistics{Goroutines: math.MaxUint32, HeapAllocatedBytes: math.MaxUint64, GCPauseTotalNS: math.MaxUint64, APIUptimeSeconds: math.MaxUint32}})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Generation string `json:"generation"`
		Heap       string `json:"heap_allocated_bytes"`
		Pause      string `json:"gc_pause_total_ns"`
		Goroutines uint32 `json:"goroutines"`
		Uptime     uint32 `json:"api_uptime_seconds"`
	}
	if json.Unmarshal(encoded, &result) != nil || result.Generation != "18446744073709551615" || result.Heap != result.Generation || result.Pause != result.Generation || result.Goroutines != math.MaxUint32 || result.Uptime != math.MaxUint32 {
		t.Fatalf("runtime integer precision lost %s", encoded)
	}
}
