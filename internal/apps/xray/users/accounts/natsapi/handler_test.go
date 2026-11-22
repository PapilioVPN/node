package natsapi_test

import (
	"context"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/natsapi"
	"github.com/nats-io/nats.go"
)

func TestMalformedSelectionNeverReachesService(t *testing.T) {
	h := natsapi.Handler{}
	for _, body := range []string{`{}`, `{"instance_id":"a"}`, `{"instance_id":null,"inbound_tag":"b"}`, `{"instance_id":"a","inbound_tag":"b","inbound_tag":"c"}`, `{"instance_id":"a","inbound_tag":"b"} {}`, `{"instance_id":"a","inbound_tag":"b","unknown":"c"}`} {
		for _, call := range []func(context.Context, []byte, nats.Header) []byte{h.Count, h.Get} {
			if result := call(context.Background(), []byte(body), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
				t.Fatal(body, string(result))
			}
		}
	}
	if result := h.Count(context.Background(), []byte(`{"instance_id":"a","inbound_tag":"b","email":"c"}`), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
		t.Fatal(string(result))
	}
	if result := h.Get(context.Background(), []byte(`{"instance_id":"a","inbound_tag":"b"}`), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
		t.Fatal(string(result))
	}
	for _, headers := range []nats.Header{{"Papilio-Deadline": {"bad"}}, {"Papilio-Deadline": {"a", "b"}}} {
		if result := h.Count(context.Background(), []byte(`{"instance_id":"a","inbound_tag":"b"}`), headers); !strings.Contains(string(result), `"error":"invalid_argument"`) {
			t.Fatal(string(result))
		}
	}
}
