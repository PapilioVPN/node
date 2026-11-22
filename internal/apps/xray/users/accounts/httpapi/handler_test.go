package httpapi_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestMalformedSelectionNeverReachesService(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{}.Register(app)
	for _, path := range []string{
		"/accounts/count", "/accounts/count?inbound_tag=a&inbound_tag=b", "/accounts/count?inbound_tag=a&email=b", "/accounts/count?inbound_tag=a&unknown=b",
		"/account?inbound_tag=a", "/account?email=a", "/account?inbound_tag=a&email=b&email=c",
	} {
		response, err := app.Test(httptest.NewRequest("GET", "/v1/xray/users/instances/a"+path, nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal(path, response.StatusCode)
		}
	}
	response, err := app.Test(httptest.NewRequest("GET", "/v1/xray/users/instances/a/accounts/count?inbound_tag=a", strings.NewReader("body")))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal(response.StatusCode)
	}
}
