package natsapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/app/services"
	"github.com/nats-io/nats.go"
)

func TestStrictInputAndShorteningDeadline(t *testing.T) {
	status := &statusReader{}
	s, err := services.New([]services.Binding{{ID: "edge"}}, status, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{Service: s, Timeout: time.Second}
	input := []byte(`{"instance_id":"edge"}`)
	for _, headers := range []nats.Header{{"Papilio-Deadline": {}}, {"Papilio-Deadline": {"bad"}}, {"Papilio-Deadline": {"a", "b"}}} {
		if r := h.Report(context.Background(), input, headers); string(r) != `{"error":"invalid_argument"}` {
			t.Fatal(string(r))
		}
	}
	if r := h.Report(context.Background(), input, nats.Header{"Papilio-Deadline": {time.Now().Add(-time.Second).Format(time.RFC3339Nano)}}); string(r) != `{"error":"deadline_exceeded"}` || status.calls != 0 {
		t.Fatal(string(r), status.calls)
	}
	if r := h.Report(context.Background(), input, nil); !strings.Contains(string(r), `"state":"disabled"`) {
		t.Fatal(string(r))
	}
}
