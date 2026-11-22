package natsapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/app/services"
	"github.com/nats-io/nats.go"
)

func TestStrictInputShortenedDeadlineAndSharedService(t *testing.T) {
	r := &reader{}
	s, _ := services.New([]services.Binding{{ID: "a", Reader: r}}, statusReader{})
	h := Handler{Service: s, Timeout: time.Second}
	ctx := context.Background()
	input := []byte(`{"instance_id":"a","email":"x"}`)
	if data := h.Count(ctx, input, nil); !strings.Contains(string(data), `"state":"untracked"`) {
		t.Fatal(string(data))
	}
	if data := h.IPs(ctx, input, nil); !strings.Contains(string(data), `"ips":[]`) {
		t.Fatal(string(data))
	}
	for _, headers := range []nats.Header{{"Papilio-Deadline": []string{"bad"}}, {"Papilio-Deadline": []string{"a", "b"}}} {
		if data := h.Count(ctx, input, headers); !strings.Contains(string(data), "invalid_argument") {
			t.Fatal(string(data))
		}
	}
	if data := h.IPs(ctx, input, nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}}); !strings.Contains(string(data), "deadline_exceeded") {
		t.Fatal(string(data))
	}
	for _, input := range []string{`{}`, `{"instance_id":"a","email":"x","email":"y"}`, `{"instance_id":"a","email":null}`} {
		if data := h.Count(ctx, []byte(input), nil); !strings.Contains(string(data), "invalid_argument") {
			t.Fatal(string(data))
		}
	}
	if r.calls != 2 {
		t.Fatal(r.calls)
	}
}
