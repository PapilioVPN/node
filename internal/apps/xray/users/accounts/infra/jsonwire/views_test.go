package jsonwire_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/jsonwire"
)

func TestViewSelectionPresenceAndMetadataNeverContainCredentialFields(t *testing.T) {
	for _, input := range []string{`{"instance_id":"a","inbound_tag":"b","page_size":null}`, `{"instance_id":"a","inbound_tag":"b","cursor":null}`, `{"instance_id":"a","inbound_tag":"b","account_type":null}`, `{"instance_id":"a","inbound_tag":"b","page_size":1,"page_size":2}`} {
		if _, err := jsonwire.DecodeSelection([]byte(input)); err == nil {
			t.Fatal(input)
		}
	}
	data, err := jsonwire.EncodeMetadataPage(domain.MetadataPage{Observation: domain.Observation{Generation: math.MaxUint64}, Users: []domain.Metadata{{Email: "subject", AccountType: "xray.proxy.vless.Account"}}})
	if err != nil || strings.Contains(string(data), "configuration") || !strings.Contains(string(data), `"18446744073709551615"`) {
		t.Fatal(string(data), err)
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(data, &result) != nil || result["read_cost"] == nil {
		t.Fatal(string(data))
	}
}
