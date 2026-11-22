package jsonwire

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
)

func TestUntrustedRequestsRejectAmbiguousShapes(t *testing.T) {
	for _, data := range []string{`null`, `[]`, `{"instance_id":"a","instance_id":"b"}`, `{"instance_id":null}`, `{"instance_id":"a","unknown":1}`, `{"instance_id":"a"} {}`, `{"instance_id":12}`} {
		if _, err := DecodeID([]byte(data)); err == nil {
			t.Fatal("accepted", data)
		}
	}
	for _, data := range []string{`{"instance_id":"a"}`, `{"context":null}`, `{"context":{"target_port":65536}}`, `{"context":{"attributes":{"x":"a","x":"b"}}}`, `{"context":{"network":1},"context":{}}`} {
		if _, err := DecodeProbe([]byte(data)); err == nil {
			t.Fatal("accepted", data)
		}
	}
	for _, data := range []string{`{"target":null}`, `{"target":"a","target":"b"}`, `{"rule_json":null}`, `{"mode":"append","confirm_destructive":null}`} {
		if strings.Contains(data, "target") {
			if _, err := DecodeOverride([]byte(data)); err == nil {
				t.Fatal("accepted", data)
			}
		} else {
			if _, err := DecodeAdd([]byte(data)); err == nil {
				t.Fatal("accepted", data)
			}
		}
	}
	r, err := DecodeOverride([]byte(`{"instance_id":"one","balancer_tag":"b","target":""}`))
	if err != nil || r.Target == nil || *r.Target != "" {
		t.Fatal(r, err)
	}
	probe, err := DecodeProbe([]byte(`{"context":{},"field_selectors":["","unknown"],"publish_result":true}`))
	if err != nil || len(probe.FieldSelectors) != 2 || !probe.PublishResult {
		t.Fatal(probe, err)
	}
}
func TestOversizedProbeRetainsResourceClassification(t *testing.T) {
	_, err := DecodeProbe([]byte(strings.Repeat(" ", (2<<20)+1)))
	if ErrorCode(err) != "resource_exhausted" {
		t.Fatal(err)
	}
}

func TestJSONEvidencePrecisionAndPrivateErrors(t *testing.T) {
	data := EncodeOperation(domain.Operation{InstanceID: "one", Generation: 1<<63 + 1, Effect: domain.Unknown, Duration: time.Duration(1<<53 + 1)}, context.DeadlineExceeded)
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	o := v["operation"].(map[string]any)
	if o["generation"] != "9223372036854775809" || o["duration_ns"] != "9007199254740993" || v["error"] != "deadline_exceeded" {
		t.Fatal(string(data))
	}
	data = EncodeProbe(domain.ProbeResult{Operation: domain.Operation{Effect: domain.Completed}, Route: &domain.Context{}}, nil)
	if strings.Contains(string(data), `"traffic_verified":true`) {
		t.Fatal(string(data))
	}
}
func FuzzRoutingEnvelope(f *testing.F) {
	for _, seed := range []string{`{}`, `null`, `{"context":{}}`, `{"rule_json":{"type":"field"},"mode":"append"}`, `{"target":""}`, `{"context":{"source_ips":["::ffff:127.0.0.1"]}}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeID(data)
		_, _ = DecodeList(data)
		_, _, _ = DecodeTag(data)
		_, _ = DecodeAdd(data)
		_, _ = DecodeProbe(data)
		_, _ = DecodeOverride(data)
	})
}
