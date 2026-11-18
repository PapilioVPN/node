package httpapi_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/identity/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestReadRequiresVerifiedIdentityBeforeCallingService(t *testing.T) {
	app := fiber.New()
	// A nil reader would panic if authentication did not precede the operation.
	httpapi.Handler{AllowRead: true, Timeout: time.Second}.Register(app)
	response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/identity", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 401 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("unauthenticated response: %d %v", response.StatusCode, response.Header)
	}
}
