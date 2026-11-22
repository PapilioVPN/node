package httpapi

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/app/services"
	"github.com/gofiber/fiber/v3"
)

func TestRoutesShapeNoStoreAndSharedService(t *testing.T) {
	r := &reader{}
	service, _ := services.New([]services.Binding{{ID: "a", Reader: r}, {ID: "disabled"}}, statusReader{})
	app := fiber.New()
	Handler{Service: service, Timeout: time.Second}.Register(app)
	for _, test := range []struct {
		method, path, body string
		status             int
		want               string
	}{
		{"GET", "/v1/xray/users/instances/a/online?email=x", "", 200, `"count":"3"`},
		{"GET", "/v1/xray/users/instances/a/online/ips?email=x", "", 200, `"last_seen_unix_seconds":"-1"`},
		{"GET", "/v1/xray/users/instances/a/online", "", 400, "invalid_argument"},
		{"GET", "/v1/xray/users/instances/a/online?email=x&email=y", "", 400, "invalid_argument"},
		{"GET", "/v1/xray/users/instances/a/online?email=x&inbound_tag=y", "", 400, "invalid_argument"},
		{"GET", "/v1/xray/users/instances/a/online?email=x", "{}", 400, "invalid_argument"},
		{"GET", "/v1/xray/users/instances/disabled/online?email=x", "", 409, "facility_not_configured"},
		{"GET", "/v1/xray/users/instances/missing/online?email=x", "", 404, "not_found"},
		{"POST", "/v1/xray/users/instances/a/online?email=x", "", 405, ""},
	} {
		response, err := app.Test(httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != test.status || !strings.Contains(string(data), test.want) {
			t.Fatal(test.path, response.StatusCode, string(data))
		}
		if test.method == "GET" && response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable online evidence")
		}
	}
	if r.calls != 2 {
		t.Fatal("invalid request performed native work", r.calls)
	}
}
