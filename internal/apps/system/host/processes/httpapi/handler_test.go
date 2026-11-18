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

	"github.com/PapilioVPN/node/internal/apps/system/host/processes/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestProcessRoutesPreserveSelectionPresenceAndPrecision(t *testing.T) {
	reader := &readerFixture{}
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader, TicksPerSecond: 100}}.Register(app)
	for _, path := range []string{"/count", "/42", "?after_pid=41&limit=1"} {
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/processes"+path, nil))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %v", body, err)
		}
		var result struct {
			Count   string
			Process *struct {
				Start string `json:"start_ticks"`
				User  string `json:"user_ticks"`
			}
			Entries []struct {
				PID     int32
				Process *json.RawMessage
				Issue   string
			}
			Next *int32 `json:"next_after_pid"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		switch path {
		case "/count":
			if result.Count != "9007199254740993" {
				t.Fatalf("lost count: %s", body)
			}
		case "/42":
			if result.Process == nil || result.Process.Start != "9007199254740993" || result.Process.User != "18446744073709551615" || reader.pid != 42 {
				t.Fatalf("lost process: %s", body)
			}
		default:
			if len(result.Entries) != 2 || result.Entries[1].Process != nil || result.Entries[1].Issue != "permission_denied" || result.Next == nil || *result.Next != 43 || reader.selection.Limit != 1 || reader.selection.AfterPID != 41 {
				t.Fatalf("lost page: %s", body)
			}
		}
	}
	if reader.calls != 3 {
		t.Fatal("unfocused reads")
	}
}

func TestProcessHTTPValidationAndSanitizedErrors(t *testing.T) {
	reader := &readerFixture{}
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader}}.Register(app)
	for _, path := range []string{"?unknown=1", "?limit=257", "?limit=-1", "?limit=1&limit=2", "?after_pid=-1", "?after_pid=2147483648", "?limit=", "/0", "/abc", "/42?limit=1", "/count?limit=1"} {
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/processes"+path, nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("%s: %d", path, response.StatusCode)
		}
	}
	if reader.calls != 0 {
		t.Fatal("invalid input reached native read")
	}
	for _, path := range []string{"", "/42", "/count"} {
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/processes"+path, strings.NewReader("{}")))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatal(response.StatusCode)
		}
		response, err = app.Test(httptest.NewRequest("POST", "/v1/system/host/processes"+path, nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 405 {
			t.Fatal(response.StatusCode)
		}
	}
	for _, failure := range []struct {
		cause  error
		status int
		code   string
	}{{domain.ErrNotFound, 404, "not_found"}, {domain.ErrPermissionDenied, 403, "permission_denied"}, {domain.ErrResourceExhausted, 503, "resource_exhausted"}, {context.Canceled, 503, "canceled"}, {context.DeadlineExceeded, 504, "deadline_exceeded"}, {errors.New("private path"), 503, "unavailable"}} {
		reader.cause = failure.cause
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/processes/42", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != failure.status || string(body) != `{"error":"`+failure.code+`"}` {
			t.Fatalf("unsafe failure: %d %s", response.StatusCode, body)
		}
	}
}
