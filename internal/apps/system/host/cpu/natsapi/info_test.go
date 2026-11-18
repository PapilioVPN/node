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

func TestNATSInfoPreservesMetadataAndAbsence(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{InfoReader: infoReaderFunc(func(context.Context) ([]domain.Processor, error) { return sampleProcessors(), nil })}}
	body := handler.ReadInfo(context.Background(), []byte("{}"), nil)
	var result struct {
		Logical    uint32           `json:"logical_count"`
		Packages   *uint32          `json:"package_count"`
		Cores      *uint32          `json:"physical_core_count"`
		Processors []map[string]any `json:"processors"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Logical != 2 || result.Packages != nil || result.Cores != nil || len(result.Processors) != 2 {
		t.Fatalf("invalid topology: %s %v", body, err)
	}
	for key, want := range map[string]any{"id": float64(4), "vendor_id": "Vendor", "family": "6", "model": "23", "model_name": "Model: processor", "stepping": "0", "microcode": "0x123", "package_id": float64(0), "core_id": float64(3), "reported_cores": float64(4), "reported_siblings": float64(8), "reported_mhz": float64(0), "cache_bytes": "18446744073709550592"} {
		if result.Processors[0][key] != want {
			t.Fatalf("%s: got %v; want %v", key, result.Processors[0][key], want)
		}
	}
	for _, key := range []string{"vendor_id", "model_name", "package_id", "core_id", "reported_mhz", "cache_bytes"} {
		if result.Processors[1][key] != nil {
			t.Fatalf("fabricated %s: %v", key, result.Processors[1])
		}
	}
}

func TestNATSInfoShapeDeadlineAndErrorMapping(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second}
	for _, input := range []string{"", "null", "[]", "{}{}", `{"unexpected":1}`, strings.Repeat(" ", 1025)} {
		var result struct {
			Error string `json:"error"`
		}
		body := handler.ReadInfo(context.Background(), []byte(input), nil)
		if err := json.Unmarshal(body, &result); err != nil || result.Error == "" {
			t.Fatalf("invalid input accepted: %s", body)
		}
	}
	for _, values := range [][]string{{"bad"}, {"bad", "duplicate"}} {
		if got := handler.ReadInfo(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": values}); string(got) != `{"error":"invalid_argument"}` {
			t.Fatal(string(got))
		}
	}
	handler.Timeout = 10 * time.Millisecond
	handler.Service.InfoReader = infoReaderFunc(func(ctx context.Context) ([]domain.Processor, error) { <-ctx.Done(); return nil, ctx.Err() })
	for _, deadline := range []time.Time{time.Now().Add(-time.Second), time.Now().Add(time.Hour)} {
		got := handler.ReadInfo(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": {deadline.UTC().Format(time.RFC3339Nano)}})
		if string(got) != `{"error":"deadline_exceeded"}` {
			t.Fatal(string(got))
		}
	}
	for _, test := range []struct {
		cause error
		code  string
	}{{context.Canceled, "canceled"}, {errors.New("private cpuinfo path"), "unavailable"}} {
		handler.Service.InfoReader = infoReaderFunc(func(context.Context) ([]domain.Processor, error) { return sampleProcessors(), test.cause })
		if got := handler.ReadInfo(context.Background(), []byte("{}"), nil); string(got) != `{"error":"`+test.code+`"}` {
			t.Fatal(string(got))
		}
	}
}
