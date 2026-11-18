package httpapi_test

import (
	"context"
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

func TestReadMapsObservationInsideTheAuthenticatedServer(t *testing.T) {
	// This standalone adapter test exercises presentation; native listener tests
	// verify the controller's client certificate before routing any operation.
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second, Service: services.Service{Reader: readerFunc(func(context.Context) (domain.Averages, error) {
		return domain.Averages{OneMinute: 1.25, FiveMinutes: 2.5, FifteenMinutes: 3.75}, nil
	})}}.Register(app)
	response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/load", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" || !strings.Contains(string(body), `"one_minute":1.25,"five_minutes":2.5,"fifteen_minutes":3.75`) {
		t.Fatalf("HTTP observation: %s, %v", body, err)
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
		response, err := app.Test(httptest.NewRequest(input.method, "/v1/system/host/load"+input.path, strings.NewReader(input.body)))
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
		httpapi.Handler{Timeout: time.Second, Service: services.Service{Reader: readerFunc(func(context.Context) (domain.Averages, error) {
			return domain.Averages{OneMinute: 1.25, FiveMinutes: 2.5, FifteenMinutes: 3.75}, test.cause
		})}}.Register(app)
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/load", nil))
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
