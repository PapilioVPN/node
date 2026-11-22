package httpapi

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/app/services"
	"github.com/gofiber/fiber/v3"
)

func TestReadOnlyRoutingShapeBoundsAndNoStore(t *testing.T) {
	status := &statusReader{}
	s, err := services.New([]services.Binding{{ID: "edge"}}, status, nil)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	(Handler{Service: s, Timeout: time.Second}).Register(app)
	root := "/v1/xray/tools/instances/edge/diagnostics"
	for _, tt := range []struct {
		path   string
		status int
	}{{root, 200}, {root + "?max_age_seconds=1", 200}, {root + "?max_age_seconds=86401", 400}, {root + "?max_age_seconds=1&max_age_seconds=2", 400}, {root + "?unknown=1", 400}, {"/v1/xray/tools/instances/missing/diagnostics", 404}} {
		response, err := app.Test(httptest.NewRequest("GET", tt.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != tt.status || response.Header.Get("Cache-Control") != "no-store" || (tt.status == 200 && !strings.Contains(string(data), `"state":"disabled"`)) {
			t.Fatal(response.Status, string(data))
		}
	}
	if status.calls != 4 {
		t.Fatal("invalid reads reached sources", status.calls)
	}
	response, err := app.Test(httptest.NewRequest("GET", root, strings.NewReader("{}")))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal(response.Status)
	}
	response, err = app.Test(httptest.NewRequest("POST", root, nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 405 {
		t.Fatal(response.Status)
	}
}
