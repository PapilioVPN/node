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

func TestHTTPUsagePreservesMeasuredAndUnknownWindows(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second, Service: services.Service{TimesReader: usageReader()}}.Register(app)
	response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/cpu/usage?per_cpu=true&interval_ns=1", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("usage response: %d %s %v", response.StatusCode, body, err)
	}
	var result struct {
		PerCPU    bool             `json:"per_cpu"`
		Requested string           `json:"requested_interval_ns"`
		CPUs      []map[string]any `json:"cpus"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if !result.PerCPU || result.Requested != "1" || len(result.CPUs) != 6 {
		t.Fatalf("lost selection/window: %s", body)
	}
	for i, want := range []struct{ cpu, state string }{{"cpu0", "measured"}, {"cpu1", "no_progress"}, {"cpu2", "counter_decreased"}, {"cpu3", "counter_overflow"}, {"cpu5", "added"}, {"cpu4", "removed"}} {
		row := result.CPUs[i]
		if row["cpu"] != want.cpu || row["state"] != want.state {
			t.Fatalf("lost CPU/state: %v", row)
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

func TestHTTPUsageRejectsInvalidInputBeforeIO(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second}.Register(app)
	for _, query := range []string{"?extra=1", "?per_cpu=bad", "?per_cpu=true&per_cpu=false", "?interval_ns=1&interval_ns=2", "?interval_ns=bad", "?interval_ns=9223372036854775808", "?interval_ns=0", "?interval_ns=-1"} {
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/cpu/usage"+query, nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("%s: %d", query, response.StatusCode)
		}
	}
	for _, test := range []struct {
		method, body string
		status       int
	}{{"GET", "{}", 400}, {"POST", "", 405}} {
		response, err := app.Test(httptest.NewRequest(test.method, "/v1/system/host/cpu/usage", strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatal(response.StatusCode)
		}
	}
}

func TestHTTPUsageDefaultsAndDeadline(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second, Service: services.Service{TimesReader: usageReader()}}.Register(app)
	response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/cpu/usage", nil))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		PerCPU    bool  `json:"per_cpu"`
		Requested int64 `json:"requested_interval_ns,string"`
	}
	err = json.NewDecoder(response.Body).Decode(&result)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || result.PerCPU || result.Requested != int64(domain.DefaultSampleInterval) {
		t.Fatalf("invalid defaults: %+v %v", result, err)
	}
	app = fiber.New()
	httpapi.Handler{Timeout: 10 * time.Millisecond, Service: services.Service{TimesReader: usageReader()}}.Register(app)
	response, err = app.Test(httptest.NewRequest("GET", "/v1/system/host/cpu/usage?interval_ns=1000000000", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 504 || string(body) != `{"error":"deadline_exceeded"}` {
		t.Fatalf("deadline did not bound sample: %d %s %v", response.StatusCode, body, err)
	}
}

func TestHTTPUsageSanitizesReadFailures(t *testing.T) {
	for _, test := range []struct {
		cause  error
		code   string
		status int
	}{{context.Canceled, "canceled", 503}, {context.DeadlineExceeded, "deadline_exceeded", 504}, {errors.New("private-stat-source"), "unavailable", 503}} {
		app := fiber.New()
		httpapi.Handler{Timeout: time.Second, Service: services.Service{TimesReader: timesReaderFunc(func(context.Context, bool) (domain.Times, error) { return domain.Times{}, test.cause })}}.Register(app)
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/cpu/usage?interval_ns=1", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != test.status || string(body) != `{"error":"`+test.code+`"}` {
			t.Fatalf("unsanitized sample failure: %d %s %v", response.StatusCode, body, err)
		}
	}
}
