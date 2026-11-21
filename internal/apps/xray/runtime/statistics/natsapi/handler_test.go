package natsapi

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestRuntimeNATSRejectsAmbiguousSelectionAndDeadline(t *testing.T) {
	h := Handler{Timeout: time.Second}
	for _, body := range []string{`{}`, `{"instance_id":"a","instance_id":"b"}`, `{"instance_id":"a","unknown":true}`} {
		if response := h.Read(context.Background(), []byte(body), nil); string(response) != `{"error":"invalid_argument"}` {
			t.Fatal(string(response))
		}
	}
	for _, header := range []nats.Header{{"Papilio-Deadline": []string{"invalid"}}, {"Papilio-Deadline": []string{"x", "y"}}} {
		if response := h.Read(context.Background(), []byte(`{"instance_id":"a"}`), header); string(response) != `{"error":"invalid_argument"}` {
			t.Fatal(string(response))
		}
	}
}
