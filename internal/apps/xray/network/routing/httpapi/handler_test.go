package httpapi

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/services"
	"github.com/gofiber/fiber/v3"
)

func TestHTTPRoutingMethodsShapesAndEvidence(t *testing.T) {
	service, err := services.New(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	Handler{Service: service, Timeout: time.Second}.Register(app)
	base := "/v1/xray/network/instances/unknown/routing"
	for _, test := range []struct {
		method, path, body, code string
		status                   int
	}{
		{"GET", "/rules?unexpected=1", "", "invalid_argument", 400},
		{"GET", "/rules?page_size=1&page_size=2", "", "invalid_argument", 400},
		{"POST", "/rules", `{"rule_json":{},"mode":"replace"}`, "invalid_argument", 400},
		{"POST", "/rules", `{"rule_json":{},"mode":"append","mode":"replace"}`, "invalid_argument", 400},
		{"PUT", "/balancers/override", `{"balancer_tag":"b"}`, "invalid_argument", 400},
		{"POST", "/tests", `{"context":{"target_port":65536}}`, "invalid_argument", 400},
		{"POST", "/tests", `{"context":{}}`, "not_found", 404},
		{"GET", "/rules", "", "not_found", 404},
	} {
		request := httptest.NewRequest(test.method, base+test.path, strings.NewReader(test.body))
		if test.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != test.status || response.Header.Get("Cache-Control") != "no-store" || !strings.Contains(string(data), test.code) {
			t.Fatalf("%s %s => %d %s", test.method, test.path, response.StatusCode, data)
		}
	}
	request := httptest.NewRequest("PATCH", base+"/rules", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 405 {
		t.Fatal("unsupported method dispatched", response.StatusCode)
	}
}
