package httpapi

import (
	"github.com/gofiber/fiber/v3"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewedRoutesRejectAmbiguousInputBeforeDispatch(t *testing.T) {
	app := fiber.New()
	Handler{}.Register(app)
	for _, test := range []struct{ path, body string }{
		{"plans", `{"sources":[]}`},
		{"plans", `{"expected_revision":"0","expected_revision":"1","sources":[]}`},
		{"plans/01234567890123456789012345678901/applications", `{"expected_revision":"0"}`},
		{"applications/01234567890123456789012345678901/recoveries", `{"recovery_id":"a"}`},
		{"applications/01234567890123456789012345678901/recoveries", `{"expected_revision":"00","recovery_id":"a"}`},
	} {
		r := httptest.NewRequest("POST", "/v1/xray/runtime/instances/a/configuration/"+test.path, strings.NewReader(test.body))
		r.Header.Set("Content-Type", "application/json")
		response, err := app.Test(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal(response.StatusCode)
		}
	}
}
