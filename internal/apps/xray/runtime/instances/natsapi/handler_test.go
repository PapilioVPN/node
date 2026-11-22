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

func TestRecoveryEnvelopeDeadlineAndBounds(t *testing.T) {
	h, r := fixture()
	for _, q := range []string{`{"instance_id":"a","max_attempts":4}`, `{"instance_id":"a","max_attempts":1,"max_attempts":2}`, `{"instance_id":"a","max_attempts":null}`} {
		if reply := h.Repair(context.Background(), []byte(q), nil); !strings.Contains(string(reply), `"invalid_argument"`) {
			t.Fatal(string(reply))
		}
	}
	deadline := nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}}
	if reply := h.Repair(context.Background(), []byte(`{"instance_id":"a","max_attempts":1}`), deadline); !strings.Contains(string(reply), "deadline_exceeded") {
		t.Fatal(string(reply))
	}
	if reply := h.List(context.Background(), []byte(`{"limit":51}`), nil); !strings.Contains(string(reply), "invalid_argument") {
		t.Fatal(string(reply))
	}
	if r.calls != 0 {
		t.Fatal("rejected requests mutated engine")
	}
}
