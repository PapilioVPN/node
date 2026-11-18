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

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/memory/httpapi"

	"github.com/gofiber/fiber/v3"
)

func TestReadMapsObservationInsideTheAuthenticatedServer(t *testing.T) {
	// This standalone adapter test exercises presentation; native listener tests
	// verify the controller's client certificate before routing any operation.
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second, Service: services.Service{Reader: readerFunc(func(context.Context) (domain.Snapshot, error) {
		return sampleMemory(), nil
	})}}.Register(app)
	response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/memory", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP observation: %s, %v", body, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"total_bytes": "18446744073709551615", "available_bytes": "3", "used_bytes": "18446744073709551612", "free_bytes": "1",
		"active_bytes": "2", "inactive_bytes": "4", "buffers_bytes": "8", "cached_bytes": "16", "shared_bytes": "32", "slab_bytes": "64", "used_percent": 99.5,
	} {
		if payload[key] != want {
			t.Fatalf("HTTP %s: got %v; want %v", key, payload[key], want)
		}
	}

}

func TestInvalidInputDoesNotCallService(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second}.Register(app)
	for _, input := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "?unknown=1", "", 400}, {"GET", "", "{}", 400}, {"POST", "", "", 405},
	} {
		response, err := app.Test(httptest.NewRequest(input.method, "/v1/system/host/memory"+input.path, strings.NewReader(input.body)))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != input.status {
			t.Fatal(response.StatusCode)
		}
	}
}

func TestReadMapsCancellationAndHidesNativeFailures(t *testing.T) {
	for _, test := range []struct {
		cause  error
		code   string
		status int
	}{
		{context.Canceled, "canceled", 503}, {context.DeadlineExceeded, "deadline_exceeded", 504},
		{errors.New("private-native-cause"), "unavailable", 503},
	} {
		app := fiber.New()
		httpapi.Handler{Timeout: time.Second, Service: services.Service{Reader: readerFunc(func(context.Context) (domain.Snapshot, error) {
			return sampleMemory(), test.cause
		})}}.Register(app)
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/memory", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != test.status || string(body) != `{"error":"`+test.code+`"}` {
			t.Fatalf("invalid HTTP failure: %s, %v", body, err)
		}
	}
}

func TestSwapRouteIsOwnedByMemory(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second, Service: services.Service{SwapReader: swapReaderFunc(func(context.Context) (domain.SwapSnapshot, error) {
		return domain.SwapSnapshot{TotalBytes: 1<<64 - 1, UsedBytes: 2, FreeBytes: 3, SwappedInBytes: 4, SwappedOutBytes: 5, UsedPercent: 6.25}, nil
	})}}.Register(app)
	response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/swap", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP swap: %d %s %v", response.StatusCode, body, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"total_bytes": "18446744073709551615", "used_bytes": "2", "free_bytes": "3", "swapped_in_bytes": "4", "swapped_out_bytes": "5", "used_percent": 6.25} {
		if payload[key] != want {
			t.Fatalf("swap %s: got %v; want %v", key, payload[key], want)
		}
	}
}

func TestSwapInputAndFailureBoundaries(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second}.Register(app)
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "?extra=1", "", 400}, {"GET", "", "{}", 400}, {"POST", "", "", 405},
	} {
		response, err := app.Test(httptest.NewRequest(test.method, "/v1/system/host/swap"+test.path, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatalf("invalid swap request: %d", response.StatusCode)
		}
	}
	for _, test := range []struct {
		cause  error
		code   string
		status int
	}{
		{context.Canceled, "canceled", 503}, {context.DeadlineExceeded, "deadline_exceeded", 504}, {errors.New("private-swap-source"), "unavailable", 503},
	} {
		app := fiber.New()
		httpapi.Handler{Timeout: time.Second, Service: services.Service{SwapReader: swapReaderFunc(func(context.Context) (domain.SwapSnapshot, error) {
			return domain.SwapSnapshot{TotalBytes: 1}, test.cause
		})}}.Register(app)
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/swap", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != test.status || string(body) != `{"error":"`+test.code+`"}` {
			t.Fatalf("swap failure: %d %s %v", response.StatusCode, body, err)
		}
	}
}
