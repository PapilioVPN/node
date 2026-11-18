package httpapi_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/domain"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestDeviceHTTPSelectionPrecisionAndErrors(t *testing.T) {
	reader := &readerFixture{}
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader}}.Register(app)
	response, err := app.Test(httptest.NewRequest("GET", "/v1/system/storage/devices?after_name=nvme0n1&limit=2", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" || reader.selection.AfterName != "nvme0n1" || reader.selection.Limit != 2 {
		t.Fatalf("%d %s", response.StatusCode, body)
	}
	for _, expected := range []string{`"read_sectors":"9007199254740993"`, `"write_sectors":"18446744073709551615"`, `"discard":null`, `"flush":null`, `"discard":{"completed":"0"`, `"milliseconds":"17"`, `"sector_size_bytes":512`, `"next_after_name":"sdb"`} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("lost %s: %s", expected, body)
		}
	}
	before := reader.calls
	for _, path := range []string{"?limit=257", "?limit=-1", "?limit=1&limit=2", "?after_name=a&after_name=b", "?unknown=1", "?after_name=" + strings.Repeat("a", 256)} {
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/storage/devices"+path, nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("%s %d", path, response.StatusCode)
		}
	}
	if reader.calls != before {
		t.Fatal("invalid selection reached native read")
	}
	for _, failure := range []struct {
		cause  error
		status int
		code   string
	}{{domain.ErrResourceExhausted, 503, "resource_exhausted"}, {context.Canceled, 503, "canceled"}, {context.DeadlineExceeded, 504, "deadline_exceeded"}, {errors.New("private path"), 503, "unavailable"}} {
		reader.cause = failure.cause
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/storage/devices", nil))
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
