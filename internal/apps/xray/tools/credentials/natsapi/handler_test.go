package natsapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/app/services"
	"github.com/nats-io/nats.go"
)

func TestDeadlineRejectsBeforeNativeAdmission(t *testing.T) {
	service, _ := services.New(nil)
	h := Handler{Service: service, Timeout: time.Second}
	for _, headers := range []nats.Header{
		{"Papilio-Deadline": []string{"invalid"}},
		{"Papilio-Deadline": []string{"a", "b"}},
		{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}},
	} {
		body := h.UUID(context.Background(), []byte(`{"instance_id":"a"}`), headers)
		if !strings.Contains(string(body), "invalid_argument") && !strings.Contains(string(body), "deadline_exceeded") {
			t.Fatal("deadline did not reject")
		}
	}
	if body := h.UUID(context.Background(), []byte(`{"instance_id":"a","seed":"private-native-material"}`), nil); !strings.Contains(string(body), "invalid_argument") || strings.Contains(string(body), "private-native-material") {
		t.Fatal("invalid envelope or secret in error")
	}
}
