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

func TestReplacementRoutesRejectAmbiguousRequestsBeforeDispatch(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{}.Register(app)
	for _, test := range []struct{ path, body string }{
		{"replacement-plans", `{}`},
		{"replacement-plans", `{"tag":"a","configuration":"AQ==","default_policy":null,"connection_policy":"retain"}`},
		{"replacement-plans", `{"tag":"a","configuration":"AQ==","sources":[],"default_policy":"preserve","connection_policy":"retain"}`},
		{"replacements", `{"plan_id":null}`},
		{"replacements", `{"plan_id":"short"}`},
	} {
		request := httptest.NewRequest("POST", "/v1/xray/proxies/instances/a/outbounds/"+test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatal(response.StatusCode)
		}
	}
}
