package natsapi

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/app/services"
	"github.com/nats-io/nats.go"
	"strings"
	"testing"
	"time"
)

func TestDeadlinesAndShapeAreSanitized(t *testing.T) {
	s, err := services.New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{Service: s, Timeout: time.Second}
	for _, headers := range []nats.Header{{"Papilio-Deadline": []string{"private-path"}}, {"Papilio-Deadline": []string{time.Now().String(), time.Now().String()}}} {
		data := h.Inspect(context.Background(), []byte(`{"instance_id":"one"}`), headers)
		if !strings.Contains(string(data), "invalid_argument") || strings.Contains(string(data), "private-path") {
			t.Fatal(string(data))
		}
	}
	past := nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}}
	data := h.Inspect(context.Background(), []byte(`{"instance_id":"one"}`), past)
	if !strings.Contains(string(data), "deadline_exceeded") {
		t.Fatal(string(data))
	}
	if data = h.Stage(context.Background(), []byte(`{"instance_id":"one","candidate":null}`), nil); !strings.Contains(string(data), "invalid_argument") {
		t.Fatal(string(data))
	}
}
