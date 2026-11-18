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

func TestCPUActivityRoutePreservesExactCounts(t *testing.T) {
	app := fiber.New()
	calls := 0
	httpapi.Handler{Timeout: time.Second, Service: services.Service{ActivityReader: activityReaderFunc(func(ctx context.Context) (domain.Activity, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded read")
		}
		calls++
		return sampleActivity(), nil
	})}}.Register(app)
	response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/cpu/activity", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var got map[string]json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || calls != 1 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid response: %d/%d", response.StatusCode, calls)
	}
	for key, want := range map[string]string{"context_switches": "9007199254740993", "interrupts": "18446744073709551615", "soft_interrupts": "200", "created_tasks": "300", "runnable_tasks": "0", "io_blocked_tasks": "2"} {
		if string(got[key]) != `"`+want+`"` {
			t.Fatalf("%s lost exact count: %s", key, got[key])
		}
	}
}

func TestHTTPActivityInputAndErrors(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{}.Register(app)
	for _, test := range []struct {
		method, suffix, body string
		code                 int
	}{{"GET", "?x=1", "", 400}, {"GET", "", "{}", 400}, {"POST", "", "", 405}} {
		response, err := app.Test(httptest.NewRequest(test.method, "/v1/system/host/cpu/activity"+test.suffix, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.code {
			t.Fatal(response.StatusCode)
		}
	}
	for _, test := range []struct {
		cause  error
		code   string
		status int
	}{{errors.New("private source"), "unavailable", 503}, {context.Canceled, "canceled", 503}, {context.DeadlineExceeded, "deadline_exceeded", 504}} {
		app := fiber.New()
		httpapi.Handler{Timeout: time.Second, Service: services.Service{ActivityReader: activityReaderFunc(func(context.Context) (domain.Activity, error) { return sampleActivity(), test.cause })}}.Register(app)
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/cpu/activity", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != test.status || string(body) != `{"error":"`+test.code+`"}` {
			t.Fatalf("unsafe failure: %s %v", body, err)
		}
	}
}
