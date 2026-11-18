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

func TestNATSUsageRejectsInvalidShapeBeforeIO(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second}
	for _, input := range []string{"", "null", "[]", "{}{}", `{"extra":1}`, `{"per_cpu":null}`, `{"per_cpu":"true"}`, `{"interval_ns":null}`, `{"interval_ns":1}`, `{"interval_ns":"bad"}`, `{"interval_ns":"9223372036854775808"}`, `{"interval_ns":"0"}`, `{"interval_ns":"-1"}`, strings.Repeat(" ", 1025)} {
		var result struct {
			Error string `json:"error"`
		}
		body := handler.SampleUsage(context.Background(), []byte(input), nil)
		if err := json.Unmarshal(body, &result); err != nil || result.Error == "" {
			t.Fatalf("invalid input accepted: %s %s", input, body)
		}
	}
	for _, values := range [][]string{{"bad"}, {"bad", "duplicate"}} {
		body := handler.SampleUsage(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": values})
		if string(body) != `{"error":"invalid_argument"}` {
			t.Fatal(string(body))
		}
	}
}

func TestNATSUsagePreservesUnknownPresenceAndMeasuredValues(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{TimesReader: usageReader()}}
	body := handler.SampleUsage(context.Background(), []byte(`{"per_cpu":true,"interval_ns":"1"}`), nil)
	var result struct {
		PerCPU    bool             `json:"per_cpu"`
		Requested string           `json:"requested_interval_ns"`
		CPUs      []map[string]any `json:"cpus"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if !result.PerCPU || result.Requested != "1" || len(result.CPUs) != 6 {
		t.Fatalf("lost sample metadata: %s", body)
	}
	for i, state := range []string{"measured", "no_progress", "counter_decreased", "counter_overflow", "added", "removed"} {
		row := result.CPUs[i]
		if row["state"] != state {
			t.Fatalf("lost state: %v", row)
		}
		if i == 0 {
			if row["percent"] != float64(25) || row["busy_ticks"] != "25" || row["total_ticks"] != "100" {
				t.Fatalf("lost measured values: %v", row)
			}
		} else if row["percent"] != nil || row["busy_ticks"] != nil || row["total_ticks"] != nil {
			t.Fatalf("fabricated unknown values: %v", row)
		}
	}
}

func TestNATSUsageDefaultsDeadlineAndFailureCodes(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{TimesReader: usageReader()}}
	body := handler.SampleUsage(context.Background(), []byte("{}"), nil)
	var defaults struct {
		PerCPU    bool  `json:"per_cpu"`
		Requested int64 `json:"requested_interval_ns,string"`
	}
	if err := json.Unmarshal(body, &defaults); err != nil || defaults.PerCPU || defaults.Requested != int64(domain.DefaultSampleInterval) {
		t.Fatalf("invalid defaults: %s %v", body, err)
	}
	handler.Timeout = 10 * time.Millisecond
	for _, deadline := range []time.Time{time.Now().Add(-time.Second), time.Now().Add(time.Hour)} {
		body = handler.SampleUsage(context.Background(), []byte(`{"interval_ns":"1000000000"}`), nats.Header{"Papilio-Deadline": {deadline.UTC().Format(time.RFC3339Nano)}})
		if string(body) != `{"error":"deadline_exceeded"}` {
			t.Fatal(string(body))
		}
	}
	for _, test := range []struct {
		cause error
		code  string
	}{{context.Canceled, "canceled"}, {errors.New("private-stat-source"), "unavailable"}} {
		handler.Service.TimesReader = timesReaderFunc(func(context.Context, bool) (domain.Times, error) { return domain.Times{}, test.cause })
		body = handler.SampleUsage(context.Background(), []byte("{}"), nil)
		if string(body) != `{"error":"`+test.code+`"}` {
			t.Fatal(string(body))
		}
	}
}

func TestNATSIdleUsageIsPresentZero(t *testing.T) {
	step := uint64(0)
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{TimesReader: timesReaderFunc(func(context.Context, bool) (domain.Times, error) {
		step++
		return domain.Times{TicksPerSecond: 100, Counters: []domain.Ticks{{CPU: "cpu-total", Idle: step}}}, nil
	})}}
	body := handler.SampleUsage(context.Background(), []byte(`{"interval_ns":"1"}`), nil)
	var result struct {
		CPUs []map[string]any `json:"cpus"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.CPUs) != 1 {
		t.Fatalf("idle sample: %s %v", body, err)
	}
	row := result.CPUs[0]
	if row["percent"] != float64(0) || row["busy_ticks"] != "0" || row["total_ticks"] != "1" || row["state"] != "measured" {
		t.Fatalf("idle zero confused with absence: %s", body)
	}
}
