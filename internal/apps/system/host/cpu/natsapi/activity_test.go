package natsapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/natsapi"
	"github.com/nats-io/nats.go"
)

func TestNATSActivityRetainsExactCountsAndBoundsInput(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{ActivityReader: activityReaderFunc(func(context.Context) (domain.Activity, error) { return sampleActivity(), nil })}}
	body := handler.ReadActivity(context.Background(), []byte("{}"), nil)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"context_switches": "9007199254740993", "interrupts": "18446744073709551615", "soft_interrupts": "200", "created_tasks": "300", "runnable_tasks": "0", "io_blocked_tasks": "2"} {
		if string(got[key]) != `"`+want+`"` {
			t.Fatalf("%s lost exact count: %s", key, got[key])
		}
	}
	handler.Service = services.Service{}
	for _, input := range []string{"", "null", "[]", "{}{}", `{"x":1}`, strings.Repeat(" ", 1025)} {
		body := handler.ReadActivity(context.Background(), []byte(input), nil)
		if string(body) != `{"error":"invalid_argument"}` && string(body) != `{"error":"resource_exhausted"}` {
			t.Fatal(string(body))
		}
	}
	if body := handler.ReadActivity(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": {"bad"}}); string(body) != `{"error":"invalid_argument"}` {
		t.Fatal(string(body))
	}
	handler.Timeout = 10 * time.Millisecond
	handler.Service.ActivityReader = activityReaderFunc(func(ctx context.Context) (domain.Activity, error) { <-ctx.Done(); return domain.Activity{}, ctx.Err() })
	for _, deadline := range []time.Time{time.Now().Add(-time.Second), time.Now().Add(time.Hour)} {
		body := handler.ReadActivity(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": {deadline.UTC().Format(time.RFC3339Nano)}})
		if string(body) != `{"error":"deadline_exceeded"}` {
			t.Fatal(string(body))
		}
	}
	handler.Service.ActivityReader = activityReaderFunc(func(context.Context) (domain.Activity, error) { return sampleActivity(), errors.New("private source") })
	if body := handler.ReadActivity(context.Background(), []byte("{}"), nil); string(body) != `{"error":"unavailable"}` {
		t.Fatal(string(body))
	}
}
