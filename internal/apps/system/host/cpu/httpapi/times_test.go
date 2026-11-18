package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestTimesHTTPPreservesTicksSelectionAndTiming(t *testing.T) {
	calls := 0
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second, Service: services.Service{TimesReader: timesReaderFunc(func(ctx context.Context, perCPU bool) (domain.Times, error) {
		if !perCPU {
			t.Error("lost per-CPU selection")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded native read")
		}
		calls++
		return sampleTimes(perCPU), nil
	})}}.Register(app)
	response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/cpu/times?per_cpu=true", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || calls != 1 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP times: %d %s %v; calls=%d", response.StatusCode, body, err, calls)
	}
	var result struct {
		PerCPU     bool                `json:"per_cpu"`
		Rate       string              `json:"ticks_per_second"`
		Counters   []map[string]string `json:"counters"`
		ObservedAt time.Time           `json:"observed_at"`
		Duration   string              `json:"duration_ns"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if !result.PerCPU || result.Rate != "100" || len(result.Counters) != 1 || result.ObservedAt.IsZero() || result.Duration == "" {
		t.Fatalf("lost times metadata: %s", body)
	}
	for key, want := range map[string]string{"cpu": "cpu9", "user_ticks": "18446744073709551615", "nice_ticks": "2", "system_ticks": "3", "idle_ticks": "4", "iowait_ticks": "5", "irq_ticks": "6", "softirq_ticks": "7", "steal_ticks": "8", "guest_ticks": "9", "guest_nice_ticks": "10"} {
		if result.Counters[0][key] != want {
			t.Fatalf("%s: %s; want %s", key, result.Counters[0][key], want)
		}
	}
}

func TestTimesHTTPRejectsInvalidShapeBeforeNativeRead(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second}.Register(app)
	for _, test := range []struct {
		method, query, body string
		status              int
	}{
		{"GET", "?unknown=true", "", 400}, {"GET", "?per_cpu=bad", "", 400}, {"GET", "?per_cpu=true&per_cpu=false", "", 400}, {"GET", "?per_cpu=%zz", "", 400}, {"GET", "", "{}", 400}, {"POST", "", "", 405},
	} {
		response, err := app.Test(httptest.NewRequest(test.method, "/v1/system/host/cpu/times"+test.query, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatalf("%s: %d", test.query, response.StatusCode)
		}
	}
}

func TestTimesHTTPDefaultsToAggregateAndSanitizesFailures(t *testing.T) {
	for _, test := range []struct {
		cause  error
		status int
		code   string
	}{{context.Canceled, 503, "canceled"}, {context.DeadlineExceeded, 504, "deadline_exceeded"}, {errors.New("private-stat-path"), 503, "unavailable"}} {
		app := fiber.New()
		httpapi.Handler{Timeout: time.Second, Service: services.Service{TimesReader: timesReaderFunc(func(_ context.Context, perCPU bool) (domain.Times, error) {
			if perCPU {
				t.Error("default selection is not aggregate")
			}
			return sampleTimes(false), test.cause
		})}}.Register(app)
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/cpu/times", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != test.status || string(body) != `{"error":"`+test.code+`"}` {
			t.Fatalf("failure: %d %s %v", response.StatusCode, body, err)
		}
	}
}
