package httpapi_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestInvalidRequestsNeverReachService(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{}.Register(app)
	for _, path := range []string{"outbounds?unknown=1", "outbounds"} {
		body := ""
		if path == "outbounds" {
			body = "unexpected"
		}
		r, err := app.Test(httptest.NewRequest("GET", "/v1/xray/proxies/instances/a/"+path, strings.NewReader(body)))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Fatal(r.StatusCode)
		}
	}
}
