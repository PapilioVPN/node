package jsonwire_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/jsonwire"
)

func TestJSONPrecisionAndOpaqueConfiguration(t *testing.T) {
	data, err := jsonwire.EncodeRecords(domain.Records{Generation: math.MaxUint64, Inbounds: []domain.Record{{Tag: "", Configuration: []byte{0, 255}}, {Tag: "", Configuration: []byte{1}}}})
	if err != nil || !strings.Contains(string(data), `"generation":"18446744073709551615"`) {
		t.Fatal(string(data), err)
	}
	var result struct {
		Inbounds []struct{ Configuration []byte }
	}
	if err := json.Unmarshal(data, &result); err != nil || len(result.Inbounds) != 2 || result.Inbounds[0].Configuration[1] != 255 {
		t.Fatal(result, err)
	}
}
func TestStrictObject(t *testing.T) {
	for _, input := range []string{`null`, `[]`, `{} {}`, `{"tag":"a","tag":"b"}`, `{"configuration":"!"}`, `{"instance_id":null}`, `{"unknown":1}`} {
		if _, err := jsonwire.Decode([]byte(input)); err == nil {
			t.Fatal(input)
		}
	}
}

func TestCatalogEnvelopePreservesPresenceAndRejectsNulls(t *testing.T) {
	for _, input := range []string{`{"page_size":null}`, `{"cursor":null}`, `{"token":null}`, `{"allow_interruption":null}`, `{"disconnect_existing":null}`, `{"page_size":1.5}`, `{"page_size":-1}`, `{"page_size":4294967296}`, `{"token":"x","token":"y"}`} {
		if _, err := jsonwire.Decode([]byte(input)); err == nil {
			t.Fatal(input)
		}
	}
	input, err := jsonwire.Decode([]byte(`{"page_size":0,"allow_interruption":false,"disconnect_existing":false}`))
	if err != nil || !input.HasPage() || input.AllowInterruption == nil || *input.AllowInterruption || input.DisconnectExisting == nil {
		t.Fatal(input, err)
	}
}
func FuzzInboundEnvelope(f *testing.F) {
	for _, seed := range []string{`{}`, `{"instance_id":"a","page_size":50}`, `{"tag":"a","configuration":"AQ==","allow_interruption":true}`, `{"token":"0123456789abcdef0123456789abcdef"}`, `{"page_size":null}`, `{"sources":[]}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = jsonwire.Decode(data) })
}
