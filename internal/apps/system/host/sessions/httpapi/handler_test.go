package httpapi_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestSessionHTTPSelectionPrecisionAndSourceBoundaries(t *testing.T) {
	logind, records := &logindFixture{}, &recordsFixture{}
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second, Service: services.Service{Logind: logind, Records: records}}.Register(app)
	for _, operation := range []struct {
		path     string
		expected []string
	}{
		{"sessions?offset=1&limit=2", []string{`"uid":0`, `"leader_pid":0`, `"idle":false`, `"next_offset":1`}},
		{"sessions/s1", []string{`"started_unix_us":"9007199254740993"`, `"remote":false`, `"remote_host":null`}},
		{"login-records?offset=1&limit=2", []string{`"record_id":"p1"`, `"started_unix_us":"-500000"`}},
	} {
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/"+operation.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %s", response.StatusCode, body)
		}
		for _, text := range operation.expected {
			if !strings.Contains(string(body), text) {
				t.Fatalf("lost %s: %s", text, body)
			}
		}
		if strings.HasPrefix(operation.path, "login-records") && strings.Count(string(body), `"record_id":"p1"`) != 2 {
			t.Fatal("lost duplicate login records")
		}
	}
	if logind.selection.Offset != 1 || logind.selection.Limit != 2 || logind.id != "s1" || records.selection.Offset != 1 {
		t.Fatal("lost operation selection")
	}
	before := logind.calls + records.calls
	for _, path := range []string{"sessions?offset=16385", "sessions?limit=257", "sessions?limit=1&limit=2", "sessions?unknown=1", "sessions/s1?offset=1", "login-records?offset=-1", "login-records?limit=257"} {
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/"+path, nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("%s: %d", path, response.StatusCode)
		}
	}
	if logind.calls+records.calls != before {
		t.Fatal("invalid input reached native readers")
	}
	for _, failure := range []struct {
		cause  error
		status int
		code   string
	}{{domain.ErrNotFound, 404, "not_found"}, {domain.ErrPermissionDenied, 403, "permission_denied"}, {domain.ErrUnsupported, 501, "unimplemented"}, {domain.ErrResourceExhausted, 503, "resource_exhausted"}, {context.DeadlineExceeded, 504, "deadline_exceeded"}, {context.Canceled, 503, "canceled"}, {errors.New("private source"), 503, "unavailable"}} {
		logind.cause = failure.cause
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/sessions/s1", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != failure.status || string(body) != `{"error":"`+failure.code+`"}` {
			t.Fatalf("%d %s", response.StatusCode, body)
		}
	}
}
