package natsapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/memory/natsapi"
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
	handler := natsapi.Handler{Timeout: 20 * time.Millisecond, Service: services.Service{Reader: readerFunc(func(ctx context.Context) (domain.Snapshot, error) {
		<-ctx.Done()
		return domain.Snapshot{}, ctx.Err()
	})}}
	for _, deadline := range []time.Time{time.Now().Add(-time.Second), time.Now().Add(time.Hour)} {
		got := handler.Read(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": {deadline.UTC().Format(time.RFC3339Nano)}})
		if string(got) != `{"error":"deadline_exceeded"}` {
			t.Fatal(string(got))
		}
	}
}

func TestReadPreservesUint64MemoryAndSanitizesFailure(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{Reader: readerFunc(func(context.Context) (domain.Snapshot, error) {
		return sampleMemory(), nil
	})}}
	result := handler.Read(context.Background(), []byte("{}"), nil)
	var observation struct {
		Total       string    `json:"total_bytes"`
		Available   string    `json:"available_bytes"`
		Free        string    `json:"free_bytes"`
		Cached      string    `json:"cached_bytes"`
		Slab        string    `json:"slab_bytes"`
		UsedPercent float64   `json:"used_percent"`
		Duration    string    `json:"duration_ns"`
		ObservedAt  time.Time `json:"observed_at"`
	}
	if err := json.Unmarshal(result, &observation); err != nil || (observation.Total != "18446744073709551615" || observation.Available != "3" || observation.Free != "1" || observation.Cached != "16" || observation.Slab != "64" || observation.UsedPercent != 99.5) || observation.Duration == "" || observation.ObservedAt.IsZero() {
		t.Fatalf("lost exact observation: %s, %v", result, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"total_bytes": "18446744073709551615", "available_bytes": "3", "used_bytes": "18446744073709551612", "free_bytes": "1",
		"active_bytes": "2", "inactive_bytes": "4", "buffers_bytes": "8", "cached_bytes": "16", "shared_bytes": "32", "slab_bytes": "64", "used_percent": 99.5,
	} {
		if payload[key] != want {
			t.Fatalf("NATS %s: got %v; want %v", key, payload[key], want)
		}
	}

	handler.Service.Reader = readerFunc(func(context.Context) (domain.Snapshot, error) {
		return sampleMemory(), errors.New("private-kernel-detail")
	})
	if got := string(handler.Read(context.Background(), []byte("{}"), nil)); got != `{"error":"unavailable"}` {
		t.Fatal(got)
	}
}
