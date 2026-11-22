package httpapi_test

import (
	"net/http/httptest"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestAccountViewShapeIsRejectedBeforeService(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{}.Register(app)
	for _, path := range []string{"/accounts", "/accounts?inbound_tag=a&page_size=51", "/accounts?inbound_tag=a&page_size=-1", "/accounts?inbound_tag=a&email=x", "/accounts/exports?inbound_tag=a&page_size=1&page_size=2", "/account/metadata?inbound_tag=a", "/accounts/compatibility?inbound_tag=a&email=x", "/accounts?inbound_tag=a&unknown=x"} {
		r, err := app.Test(httptest.NewRequest("GET", "/v1/xray/users/instances/a"+path, nil))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 400 || r.Header.Get("Cache-Control") != "no-store" {
			t.Fatal(path, r.StatusCode)
		}
	}
}
