package httpapi_test

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestNodeHTTPIdentityPrecisionPresenceAndErrors(t *testing.T) {
	reader := &reader{}
	app := fiber.New()
	httpapi.Handler{Service: services.Service{Reader: reader, Probes: reader}, Timeout: time.Second}.Register(app)
	for _, test := range []struct {
		path string
		want []string
	}{
		{"", []string{`"pid":42`, `"modified":false`, `"revision":null`}},
		{"/resources", []string{`"start_ticks":"9007199254740993"`, `"heap_objects_bytes":"0"`}},
		{"/diagnostics", []string{`"healthy":false`, `"ready":true`, `"grpc":null`, `"round_trip_ns":null`}},
	} {
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/node/runtime"+test.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %s", response.StatusCode, body)
		}
		for _, want := range test.want {
			if !strings.Contains(string(body), want) {
				t.Fatalf("lost %s in %s", want, body)
			}
		}
		if strings.Contains(string(body), "private native cause") {
			t.Fatal("leaked private native error")
		}
		before := reader.calls
		response, err = app.Test(httptest.NewRequest("GET", "/v1/system/node/runtime"+test.path+"?extra=1", nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 || reader.calls != before {
			t.Fatal("invalid input reached reader")
		}
	}
	reader.cause = context.DeadlineExceeded
	response, err := app.Test(httptest.NewRequest("GET", "/v1/system/node/runtime/resources", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 504 {
		t.Fatal(response.StatusCode)
	}
}
