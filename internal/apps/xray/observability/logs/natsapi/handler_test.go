package natsapi_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/natsapi"
	"github.com/nats-io/nats.go"
)

func TestDeadlineAndStrictPullEnvelope(t *testing.T) {
	s, err := services.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := natsapi.Handler{Service: s, Timeout: time.Second}
	request := []byte(`{"instance_id":"edge"}`)
	for _, headers := range []nats.Header{{"Papilio-Deadline": []string{"bad"}}, {"Papilio-Deadline": []string{"a", "b"}}} {
		if string(h.Tail(context.Background(), request, headers)) != `{"error":"invalid_argument"}` {
			t.Fatal(headers)
		}
	}
	data := h.Tail(context.Background(), request, nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}})
	if !strings.Contains(string(data), "deadline_exceeded") {
		t.Fatal(string(data))
	}
	if string(h.Follow(context.Background(), request, nil)) != `{"error":"invalid_argument"}` {
		t.Fatal("missing cursor accepted")
	}
}
