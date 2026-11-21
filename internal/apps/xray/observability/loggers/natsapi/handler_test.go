package natsapi

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestLoggerNATSRejectsAmbiguousIdentityAndDeadline(t *testing.T) {
	handler := Handler{Timeout: time.Second}
	for _, input := range []string{`{}`, `{"instance_id":"a","instance_id":"b"}`, `{"instance_id":"a","unknown":1}`} {
		if result := handler.Reopen(context.Background(), []byte(input), nil); string(result) != `{"error":"invalid_argument"}` {
			t.Fatal(string(result))
		}
	}
	for _, headers := range []nats.Header{{"Papilio-Deadline": []string{"invalid"}}, {"Papilio-Deadline": []string{"a", "b"}}} {
		if result := handler.Status(context.Background(), []byte(`{"instance_id":"a"}`), headers); string(result) != `{"error":"invalid_argument"}` {
			t.Fatal(string(result))
		}
	}
}
