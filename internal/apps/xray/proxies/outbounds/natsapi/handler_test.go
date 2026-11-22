package natsapi_test

import (
	"context"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/natsapi"
	"github.com/nats-io/nats.go"
)

func TestInvalidEnvelopesNeverReachService(t *testing.T) {
	h := natsapi.Handler{}
	for _, body := range []string{`{}`, `{"instance_id":null}`, `{"instance_id":"a","instance_id":"b"}`, `{"instance_id":"a","tag":"b"}`, `{"instance_id":"a"} {}`} {
		if result := h.Records(context.Background(), []byte(body), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
			t.Fatal(string(result))
		}
	}
	if result := h.Records(context.Background(), []byte(`{"instance_id":"a"}`), nats.Header{"Papilio-Deadline": {"invalid"}}); !strings.Contains(string(result), `"error":"invalid_argument"`) {
		t.Fatal(string(result))
	}
}

func TestInvalidControlEnvelopesNeverReachService(t *testing.T) {
	h := natsapi.Handler{}
	for _, test := range []struct {
		call func(context.Context, []byte, nats.Header) []byte
		body string
	}{{h.Add, `{"instance_id":"a","tag":"b"}`}, {h.Remove, `{"instance_id":"a"}`}, {h.AddSources, `{"instance_id":"a","sources":[]}`}, {h.Status, `{"instance_id":"a","configuration":"AQ=="}`}} {
		if result := test.call(context.Background(), []byte(test.body), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
			t.Fatal(string(result))
		}
	}
}
