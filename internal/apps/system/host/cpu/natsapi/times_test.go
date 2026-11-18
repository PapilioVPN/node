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

func TestTimesNATSRejectsInvalidShapeBeforeService(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second}
	for _, input := range []string{"", "null", "[]", `{"unknown":true}`, `{"per_cpu":null}`, `{"per_cpu":"true"}`, "{}{}", strings.Repeat(" ", 1025)} {
		var result struct {
			Error string `json:"error"`
		}
		reply := handler.ReadTimes(context.Background(), []byte(input), nil)
		if err := json.Unmarshal(reply, &result); err != nil || result.Error == "" {
			t.Fatalf("invalid shape accepted: %s %s", input, reply)
		}
	}
	for _, values := range [][]string{{"bad"}, {"bad", "duplicate"}} {
		if got := handler.ReadTimes(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": values}); string(got) != `{"error":"invalid_argument"}` {
			t.Fatal(string(got))
		}
	}
}

func TestTimesNATSDefaultsAndAllCounterPrecision(t *testing.T) {
	for _, input := range []string{"{}", `{"per_cpu":false}`, `{"per_cpu":true}`} {
		perCPU := strings.Contains(input, "true")
		calls := 0
		handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{TimesReader: timesReaderFunc(func(ctx context.Context, selection bool) (domain.Times, error) {
			if selection != perCPU {
				t.Error("lost selection")
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Error("unbounded read")
			}
			calls++
			return sampleTimes(perCPU), nil
		})}}
		reply := handler.ReadTimes(context.Background(), []byte(input), nil)
		var result struct {
			PerCPU     bool                `json:"per_cpu"`
			Rate       string              `json:"ticks_per_second"`
			Counters   []map[string]string `json:"counters"`
			ObservedAt time.Time           `json:"observed_at"`
			Duration   string              `json:"duration_ns"`
		}
		if err := json.Unmarshal(reply, &result); err != nil {
			t.Fatal(err)
		}
		if calls != 1 || result.PerCPU != perCPU || result.Rate != "100" || len(result.Counters) != 1 || result.ObservedAt.IsZero() || result.Duration == "" {
			t.Fatalf("lost measured result: %s", reply)
		}
		for key, want := range map[string]string{"cpu": "cpu9", "user_ticks": "18446744073709551615", "nice_ticks": "2", "system_ticks": "3", "idle_ticks": "4", "iowait_ticks": "5", "irq_ticks": "6", "softirq_ticks": "7", "steal_ticks": "8", "guest_ticks": "9", "guest_nice_ticks": "10"} {
			if result.Counters[0][key] != want {
				t.Fatalf("%s: %s; want %s", key, result.Counters[0][key], want)
			}
		}
	}
}

func TestTimesNATSDeadlineAndFailureBoundaries(t *testing.T) {
	handler := natsapi.Handler{Timeout: 20 * time.Millisecond, Service: services.Service{TimesReader: timesReaderFunc(func(ctx context.Context, _ bool) (domain.Times, error) {
		<-ctx.Done()
		return domain.Times{}, ctx.Err()
	})}}
	for _, deadline := range []time.Time{time.Now().Add(-time.Second), time.Now().Add(time.Hour)} {
		got := handler.ReadTimes(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": {deadline.UTC().Format(time.RFC3339Nano)}})
		if string(got) != `{"error":"deadline_exceeded"}` {
			t.Fatal(string(got))
		}
	}
	for _, test := range []struct {
		cause error
		code  string
	}{{context.Canceled, "canceled"}, {errors.New("private-stat-path"), "unavailable"}} {
		handler.Service.TimesReader = timesReaderFunc(func(context.Context, bool) (domain.Times, error) { return sampleTimes(false), test.cause })
		if got := handler.ReadTimes(context.Background(), []byte("{}"), nil); string(got) != `{"error":"`+test.code+`"}` {
			t.Fatal(string(got))
		}
	}
}
