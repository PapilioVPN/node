package natsapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/load/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/load/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/load/natsapi"
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
	handler := natsapi.Handler{Timeout: 20 * time.Millisecond, Service: services.Service{Reader: readerFunc(func(ctx context.Context) (domain.Averages, error) {
		<-ctx.Done()
		return domain.Averages{}, ctx.Err()
	})}}
	for _, deadline := range []time.Time{time.Now().Add(-time.Second), time.Now().Add(time.Hour)} {
		got := handler.Read(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": {deadline.UTC().Format(time.RFC3339Nano)}})
		if string(got) != `{"error":"deadline_exceeded"}` {
			t.Fatal(string(got))
		}
	}
}

func TestReadPreservesLoadWindowsAndSanitizesFailure(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{Reader: readerFunc(func(context.Context) (domain.Averages, error) {
		return domain.Averages{OneMinute: 1.25, FiveMinutes: 2.5, FifteenMinutes: 3.75}, nil
	})}}
	result := handler.Read(context.Background(), []byte("{}"), nil)
	var observation struct {
		OneMinute      float64   `json:"one_minute"`
		FiveMinutes    float64   `json:"five_minutes"`
		FifteenMinutes float64   `json:"fifteen_minutes"`
		Duration       string    `json:"duration_ns"`
		ObservedAt     time.Time `json:"observed_at"`
	}
	if err := json.Unmarshal(result, &observation); err != nil || (observation.OneMinute != 1.25 || observation.FiveMinutes != 2.5 || observation.FifteenMinutes != 3.75) || observation.Duration == "" || observation.ObservedAt.IsZero() {
		t.Fatalf("lost exact observation: %s, %v", result, err)
	}
	handler.Service.Reader = readerFunc(func(context.Context) (domain.Averages, error) {
		return domain.Averages{OneMinute: 1}, errors.New("private-kernel-detail")
	})
	if got := string(handler.Read(context.Background(), []byte("{}"), nil)); got != `{"error":"unavailable"}` {
		t.Fatal(got)
	}
}
