package httpapi

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/app/services"
	"github.com/gofiber/fiber/v3"
)

func TestResourcesMethodsStrictShapeAndPrivateResponse(t *testing.T) {
	g := &generator{}
	s, _ := services.New([]services.Binding{{ID: "a", Generator: g}})
	app := fiber.New()
	Handler{Service: s, Timeout: time.Second}.Register(app)
	for _, test := range []struct {
		method, path, body string
		status             int
		want               string
	}{
		{"POST", "uuids", `{}`, 200, "fixture-private"},
		{"POST", "uuids?command=run", `{}`, 400, "invalid_argument"},
		{"POST", "uuids", `{"command":"run"}`, 400, "invalid_argument"},
		{"POST", "uuids", `{"instance_id":"a"}`, 400, "invalid_argument"},
		{"POST", "uuids", `{"name":null}`, 400, "invalid_argument"},
		{"POST", "tls-certificates", `{"file":"/tmp/private"}`, 400, "invalid_argument"},
		{"POST", "ech-keysets", `{"pem":true}`, 400, "invalid_argument"},
		{"GET", "uuids", ``, 405, ""},
	} {
		r, err := app.Test(httptest.NewRequest(test.method, "/v1/xray/tools/instances/a/credentials/"+test.path, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != test.status || !strings.Contains(string(data), test.want) {
			t.Fatal("HTTP contract failure", test.path, r.StatusCode)
		}
		if test.method == "POST" && r.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable credential response")
		}
		if test.status != 200 && strings.Contains(string(data), "fixture-private") {
			t.Fatal("credential on failure")
		}
	}
	if g.calls != 1 {
		t.Fatal("invalid requests performed native work")
	}
}
