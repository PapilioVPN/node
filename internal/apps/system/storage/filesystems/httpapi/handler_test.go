package httpapi_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/domain"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestFilesystemRoutesPreserveSelectionCapacityAndErrors(t *testing.T) {
	reader := &readerFixture{}
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader}}.Register(app)
	for _, path := range []string{"?after_id=41&limit=1", "/usage?path=%2Fpath%20with%20spaces"} {
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/storage/filesystems"+path, nil))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %s", response.StatusCode, body)
		}
		if strings.HasPrefix(path, "?") {
			if reader.selection.AfterID != 41 || reader.selection.Limit != 1 || !strings.Contains(string(body), `"read_only":true`) || !strings.Contains(string(body), `"next_after_id":42`) {
				t.Fatalf("lost mount selection: %s", body)
			}
		} else if reader.path != "/path with spaces" || !strings.Contains(string(body), `"total_bytes":"9007199254740993"`) || !strings.Contains(string(body), `"reserved_bytes":"12288"`) {
			t.Fatalf("lost capacity: %s", body)
		}
	}
	before := reader.calls
	for _, path := range []string{"?limit=257", "?after_id=-1", "?after_id=4294967296", "?limit=1&limit=2", "?unknown=1", "/usage", "/usage?path=relative", "/usage?path=/&path=/tmp", "/usage?path=/&unknown=1"} {
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/storage/filesystems"+path, nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("%s: %d", path, response.StatusCode)
		}
	}
	if reader.calls != before {
		t.Fatal("invalid input reached native I/O")
	}
	for _, failure := range []struct {
		cause  error
		status int
		code   string
	}{{domain.ErrNotFound, 404, "not_found"}, {domain.ErrPermissionDenied, 403, "permission_denied"}, {domain.ErrResourceExhausted, 503, "resource_exhausted"}, {context.Canceled, 503, "canceled"}, {context.DeadlineExceeded, 504, "deadline_exceeded"}, {errors.New("private path"), 503, "unavailable"}} {
		reader.cause = failure.cause
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/storage/filesystems/usage?path=/", nil))
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
