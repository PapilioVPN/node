package httpapi

import (
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/app/services"
	"github.com/gofiber/fiber/v3"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAssetRoutesStrictInputAndOperationalEnvelope(t *testing.T) {
	s, err := services.New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	Handler{Service: s, Timeout: time.Second}.Register(app)
	root := "/v1/xray/tools/instances/one/assets"
	for _, tc := range []struct {
		method, path, body string
		status             int
		code               string
	}{
		{"GET", root, "", 404, "not_found"},
		{"GET", root + "?ignored=1", "", 400, "invalid_argument"},
		{"GET", root, "{}", 400, "invalid_argument"},
		{"POST", root + "/candidates", `{"candidate":null}`, 400, "invalid_argument"},
		{"POST", root + "/activations", `{"expected_revision":"1"}`, 400, "invalid_argument"},
		{"POST", root + "/activations", `{"expected_revision":"1","execution_id":"` + strings.Repeat("a", 32) + `","release_id":"` + strings.Repeat("b", 64) + `"}`, 200, "not_found"},
	} {
		r, err := app.Test(httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != tc.status || !strings.Contains(string(data), tc.code) || r.Header.Get("Cache-Control") != "no-store" {
			t.Fatal(r.StatusCode, string(data), r.Header)
		}
	}
}
