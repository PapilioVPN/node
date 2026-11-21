package natsapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestStrictSelectionDeadlinesAndPartialRestart(t *testing.T) {
	h, r := fixture()
	for _, input := range []string{`null`, `{}`, `{"instance_id":"a","instance_id":"a"}`, `{"instance_id":"a","extra":1}`, `{"instance_id":"a"} {}`, `{"instance_id":null}`, `{"instance_id":"../a"}`} {
		data := h.Start(context.Background(), []byte(input), nil)
		if !strings.Contains(string(data), `"effect":"not_applied"`) || !strings.Contains(string(data), `"error":"invalid_argument"`) {
			t.Fatalf("accepted %s: %s", input, data)
		}
	}
	deadline := nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}}
	data := h.Start(context.Background(), []byte(`{"instance_id":"a"}`), deadline)
	if !strings.Contains(string(data), "deadline_exceeded") || r.calls != 0 {
		t.Fatalf("deadline mutated: %s", data)
	}
	r.fail = true
	data = h.Restart(context.Background(), []byte(`{"instance_id":"a"}`), nil)
	if !strings.Contains(string(data), `"effect":"partial"`) || strings.Contains(string(data), "secret") {
		t.Fatalf("partial restart: %s", data)
	}
}
