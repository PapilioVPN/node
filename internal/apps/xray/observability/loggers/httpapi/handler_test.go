package httpapi

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

func TestLoggerHTTPRejectsUnexpectedInputBeforeDispatch(t *testing.T) {
	app := fiber.New()
	(Handler{Timeout: time.Second}).Register(app)
	for _, test := range []struct{ method, path, body string }{{"POST", "reopenings", `{}`}, {"POST", "reopenings?unknown=1", ``}, {"GET", "control-status?unknown=1", ``}, {"GET", "control-status", `{"instance_id":"b"}`}} {
		response, err := app.Test(httptest.NewRequest(test.method, "/v1/xray/observability/instances/a/logger/"+test.path, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("invalid HTTP shape reached logger control")
		}
	}
}
func TestLoggerHTTPUnknownTimeoutRetainsProgress(t *testing.T) {
	app := fiber.New()
	app.Post("/test", func(request fiber.Ctx) error {
		code, status := errorStatus(context.DeadlineExceeded)
		request.Status(status)
		data, err := jsonwire.EncodeOperation(domain.Operation{Effect: domain.Unknown, RequiresInstanceRestart: true}, code)
		return send(request, data, err)
	})
	response, err := app.Test(httptest.NewRequest("POST", "/test", nil))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 504 || !strings.Contains(string(data), `"effect":"unknown"`) || !strings.Contains(string(data), `"requires_instance_restart":true`) {
		t.Fatalf("timeout lost effects %d %s", response.StatusCode, data)
	}
}
