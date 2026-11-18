package natsapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/uptime/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/uptime/natsapi"
	"github.com/nats-io/nats.go"
)

func TestReadRejectsInvalidRequestsBeforeService(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second}
	for _, input := range []string{"", "null", "[]", `{"unknown":1}`, "{}{}", strings.Repeat(" ", 1025)} {
		result := handler.Read(context.Background(), []byte(input), nil)
		var response struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(result, &response); err != nil || response.Error == "" {
			t.Fatalf("invalid request accepted: %q %s", input, result)
		}
	}
	for _, values := range [][]string{{"invalid"}, {"invalid", "duplicate"}} {
		if got := string(handler.Read(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": values})); got != `{"error":"invalid_argument"}` {
			t.Fatal(got)
		}
	}
}

func TestCallerDeadlineOnlyShortensConfiguredTimeout(t *testing.T) {
	handler := natsapi.Handler{Timeout: 20 * time.Millisecond, Service: services.Service{Reader: readerFunc(func(ctx context.Context) (time.Duration, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})}}
	for _, deadline := range []time.Time{time.Now().Add(-time.Second), time.Now().Add(time.Hour)} {
		got := handler.Read(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": {deadline.UTC().Format(time.RFC3339Nano)}})
		if string(got) != `{"error":"deadline_exceeded"}` {
			t.Fatal(string(got))
		}
	}
}

func TestReadPreservesIntegerPrecisionAndSanitizesFailure(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{Reader: readerFunc(func(context.Context) (time.Duration, error) { return time.Duration(1<<63 - 1), nil })}}
	result := handler.Read(context.Background(), []byte("{}"), nil)
	var observation struct {
		Uptime     string    `json:"uptime_ns"`
		Duration   string    `json:"duration_ns"`
		ObservedAt time.Time `json:"observed_at"`
	}
	if err := json.Unmarshal(result, &observation); err != nil || observation.Uptime != "9223372036854775807" || observation.Duration == "" || observation.ObservedAt.IsZero() {
		t.Fatalf("lost exact observation: %s, %v", result, err)
	}
	handler.Service.Reader = readerFunc(func(context.Context) (time.Duration, error) { return time.Hour, errors.New("private-kernel-detail") })
	if got := string(handler.Read(context.Background(), []byte("{}"), nil)); got != `{"error":"unavailable"}` {
		t.Fatal(got)
	}
}
