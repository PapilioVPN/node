package natsapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/services"
	"github.com/nats-io/nats.go"
)

func TestNATSRequestDeadlineAndExplicitClear(t *testing.T) {
	service, err := services.New(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{Service: service, Timeout: time.Second}
	ctx := context.Background()
	for _, test := range []struct {
		data    string
		headers nats.Header
		code    string
	}{
		{`{"instance_id":"unknown","balancer_tag":"b"}`, nil, "invalid_argument"},
		{`{"instance_id":"unknown","balancer_tag":"b","target":""}`, nil, "not_found"},
		{`{"instance_id":"unknown","balancer_tag":"b","target":""}`, nats.Header{"Papilio-Deadline": []string{"bad"}}, "invalid_argument"},
		{`{"instance_id":"unknown","balancer_tag":"b","target":""}`, nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}}, "deadline_exceeded"},
	} {
		result := h.Override(ctx, []byte(test.data), test.headers)
		if !strings.Contains(string(result), test.code) {
			t.Fatal(string(result))
		}
	}
	if result := h.List(ctx, []byte(`{"instance_id":"unknown","page_size":51}`), nil); !strings.Contains(string(result), "invalid_argument") {
		t.Fatal(string(result))
	}
}
