package natsapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/app/services"
	"github.com/nats-io/nats.go"
)

func TestPagedSubscriptionsUseOneObservationAndDeadline(t *testing.T) {
	r := &reader{}
	s, _ := services.New([]services.Binding{{ID: "a", Reader: r, Collection: r}}, statusReader{})
	h := Handler{Service: s, Timeout: time.Second}
	for _, operation := range []func(context.Context, []byte, nats.Header) []byte{h.List, h.Records} {
		input := []byte(`{"instance_id":"a"}`)
		first := operation(context.Background(), input, nil)
		var envelope struct {
			Page struct {
				Next string `json:"next_page_token"`
			}
			Records []json.RawMessage
		}
		if err := json.Unmarshal(first, &envelope); err != nil || len(envelope.Records) != 50 || envelope.Page.Next == "" {
			t.Fatal(string(first), err)
		}
		next, _ := json.Marshal(map[string]string{"instance_id": "a", "page_token": envelope.Page.Next})
		if reply := operation(context.Background(), next, nil); !strings.Contains(string(reply), `"fresh":false`) || !strings.Contains(string(reply), "email-50") {
			t.Fatal(string(reply))
		}
		if reply := operation(context.Background(), input, nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}}); !strings.Contains(string(reply), "deadline_exceeded") {
			t.Fatal(string(reply))
		}
	}
	if r.calls != 2 {
		t.Fatal("extra reads", r.calls)
	}
}
