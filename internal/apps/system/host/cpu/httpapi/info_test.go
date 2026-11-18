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

func TestHTTPInfoPreservesMetadataAndUnknownFields(t *testing.T) {
	app := fiber.New()
	calls := 0
	httpapi.Handler{Timeout: time.Second, Service: services.Service{InfoReader: infoReaderFunc(func(ctx context.Context) ([]domain.Processor, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded inventory")
		}
		calls++
		return sampleProcessors(), nil
	})}}.Register(app)
	response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/cpu/info", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || calls != 1 || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("inventory: %s %v; calls=%d", body, err, calls)
	}
	var result struct {
		Logical    uint32           `json:"logical_count"`
		Packages   *uint32          `json:"package_count"`
		Cores      *uint32          `json:"physical_core_count"`
		Processors []map[string]any `json:"processors"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Logical != 2 || result.Packages != nil || result.Cores != nil || len(result.Processors) != 2 {
		t.Fatalf("invalid topology: %s %v", body, err)
	}
	for key, want := range map[string]any{"id": float64(4), "vendor_id": "Vendor", "family": "6", "model": "23", "model_name": "Model: processor", "stepping": "0", "microcode": "0x123", "package_id": float64(0), "core_id": float64(3), "reported_cores": float64(4), "reported_siblings": float64(8), "reported_mhz": float64(0), "cache_bytes": "18446744073709550592"} {
		if result.Processors[0][key] != want {
			t.Fatalf("%s: got %v; want %v", key, result.Processors[0][key], want)
		}
	}
	for _, key := range []string{"vendor_id", "model_name", "package_id", "core_id", "reported_mhz", "cache_bytes"} {
		if result.Processors[1][key] != nil {
			t.Fatalf("fabricated %s: %v", key, result.Processors[1])
		}
	}
}

func TestHTTPInfoRejectsShapeAndSanitizesFailures(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second}.Register(app)
	for _, test := range []struct {
		method, query, body string
		status              int
	}{{"GET", "?unknown=1", "", 400}, {"GET", "", "{}", 400}, {"POST", "", "", 405}} {
		response, err := app.Test(httptest.NewRequest(test.method, "/v1/system/host/cpu/info"+test.query, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatal(response.StatusCode)
		}
	}
	for _, test := range []struct {
		cause  error
		code   string
		status int
	}{{context.Canceled, "canceled", 503}, {context.DeadlineExceeded, "deadline_exceeded", 504}, {errors.New("private cpuinfo path"), "unavailable", 503}} {
		app := fiber.New()
		httpapi.Handler{Timeout: time.Second, Service: services.Service{InfoReader: infoReaderFunc(func(context.Context) ([]domain.Processor, error) { return sampleProcessors(), test.cause })}}.Register(app)
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/cpu/info", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != test.status || string(body) != `{"error":"`+test.code+`"}` {
			t.Fatalf("inventory failure: %s %v", body, err)
		}
	}
}
