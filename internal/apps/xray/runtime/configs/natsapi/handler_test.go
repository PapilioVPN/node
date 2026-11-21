package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/nats-io/nats.go"
)

type builderFixture struct{ calls int }

func (b *builderFixture) Validate(context.Context, domain.Candidate) (domain.Validation, error) {
	b.calls++
	return domain.Validation{Failure: errors.New("private native credential")}, nil
}
func TestStrictEnvelopeDeadlineAndPrivateNativeCauses(t *testing.T) {
	b := &builderFixture{}
	service, _ := services.New([]services.Binding{{ID: "a", Builder: b}})
	h := Handler{Service: service, Timeout: time.Second}
	const valid = `{"instance_id":"a","format":"json","content":"e30="}`
	for _, data := range []string{`null`, `[]`, `{}`, `{"instance_id":"a","instance_id":"other","format":"json","content":"e30="}`, `{"instance_id":"a","format":"json","format":"yaml","content":"e30="}`, `{"instance_id":"a","format":"json","content":"invalid-base64"}`, valid + ` {}`, strings.Replace(valid, `"content"`, `"unknown"`, 1)} {
		got := h.Validate(context.Background(), []byte(data), nil)
		if !strings.Contains(string(got), `"error":"invalid_argument"`) {
			t.Fatalf("invalid envelope accepted: %s => %s", data, got)
		}
	}
	for _, headers := range []nats.Header{{"Papilio-Deadline": []string{"bad"}}, {"Papilio-Deadline": []string{time.Now().Format(time.RFC3339Nano), time.Now().Format(time.RFC3339Nano)}}} {
		if got := h.Validate(context.Background(), []byte(valid), headers); !strings.Contains(string(got), `"error":"invalid_argument"`) {
			t.Fatalf("invalid deadline accepted: %s", got)
		}
	}
	got := h.Validate(context.Background(), []byte(valid), nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}})
	if !strings.Contains(string(got), `"error":"deadline_exceeded"`) || b.calls != 0 {
		t.Fatalf("expired input reached native builder: %s", got)
	}
	got = h.Validate(context.Background(), []byte(valid), nil)
	var result struct {
		InstanceID string    `json:"instance_id"`
		Valid      bool      `json:"valid"`
		CheckedAt  time.Time `json:"checked_at"`
		Duration   string    `json:"duration_ns"`
	}
	if json.Unmarshal(got, &result) != nil || result.InstanceID != "a" || result.Valid || result.CheckedAt.IsZero() || result.Duration == "" || strings.Contains(string(got), "private") || b.calls != 1 {
		t.Fatalf("result %s", got)
	}
}
