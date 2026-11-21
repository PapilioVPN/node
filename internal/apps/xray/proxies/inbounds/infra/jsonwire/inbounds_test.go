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
