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
