package natsapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/app/services"
	"github.com/nats-io/nats.go"
)

func TestStrictRequestAndShorteningDeadline(t *testing.T) {
	r := &reader{}
	s, err := services.New([]services.Binding{{ID: "edge", Reader: r}}, running{})
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{Service: s, Timeout: time.Second}
	input := []byte(`{"instance_id":"edge","outbound_tag":""}`)
	for _, header := range []nats.Header{{"Papilio-Deadline": {}}, {"Papilio-Deadline": {"bad"}}, {"Papilio-Deadline": {"a", "b"}}} {
		if got := h.Get(context.Background(), input, header); string(got) != `{"error":"invalid_argument"}` {
			t.Fatal(string(got))
		}
	}
	if got := h.Get(context.Background(), input, nats.Header{"Papilio-Deadline": {time.Now().Add(-time.Second).Format(time.RFC3339Nano)}}); string(got) != `{"error":"deadline_exceeded"}` || r.calls != 0 {
		t.Fatal(string(got), r.calls)
	}
	if got := h.Get(context.Background(), input, nats.Header{"Papilio-Deadline": {time.Now().Add(time.Hour).Format(time.RFC3339Nano)}}); !strings.Contains(string(got), `"status":null`) || r.calls != 1 {
		t.Fatal(string(got), r.calls)
	}
	if got := h.List(context.Background(), []byte(`{"instance_id":"edge","page_size":51}`), nil); string(got) != `{"error":"invalid_argument"}` || r.calls != 1 {
		t.Fatal(string(got), r.calls)
	}
	if got := h.List(context.Background(), []byte(`{"instance_id":"edge"}`), nil); !strings.Contains(string(got), `"statuses":[]`) {
		t.Fatal(string(got))
	}
}
