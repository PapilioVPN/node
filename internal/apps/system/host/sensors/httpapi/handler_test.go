package httpapi_test

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestSensorRoutingPrecisionPresenceInputAndDeadline(t *testing.T) {
	r := &reader{}
	app := fiber.New()
	httpapi.Handler{Service: services.Service{Reader: r}, Timeout: time.Second}.Register(app)
	for _, operation := range []struct {
		path     string
		expected []string
	}{
		{"temperatures", []string{`"milli_celsius":"-12500"`, `"high_milli_celsius":"0"`, `"critical_milli_celsius":null`}},
		{"fans", []string{`"rpm":"9007199254740993"`}},
		{"power-supplies", []string{`"present":false`, `"capacity_percent":0`, `"online":null`}},
	} {
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/sensors/"+operation.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %s", response.StatusCode, data)
		}
		for _, want := range operation.expected {
			if !strings.Contains(string(data), want) {
				t.Fatalf("lost %s: %s", want, data)
			}
		}
		before := r.calls
		response, err = app.Test(httptest.NewRequest("GET", "/v1/system/host/sensors/"+operation.path+"?extra=1", nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 || r.calls != before {
			t.Fatal("invalid request read native source")
		}
	}
	r.cause = context.DeadlineExceeded
	response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/sensors/fans", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 504 {
		t.Fatal(response.StatusCode)
	}
}
