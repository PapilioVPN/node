package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/services"
	"github.com/gofiber/fiber/v3"
)

func TestResetRoutesRejectAmbiguousIntentBeforeEffect(t *testing.T) {
	reader := &readerFixture{}
	service, _ := services.New([]services.Binding{{ID: "a", Resetter: reader}}, statusFixture{})
	app := fiber.New()
	(Handler{Service: service, Timeout: time.Second}).Register(app)
	initial, _ := service.ResetStatus(context.Background(), "a", 0, 0)
	valid := fmt.Sprintf(`{"epoch":%q,"operation_id":"op","expected_revision":"0","scope":"user","name":"A>>>B","direction":"uplink"}`, initial.Epoch)
	for _, test := range []struct {
		path, body string
		want       int
	}{
		{"counter-resets", strings.Replace(valid, `"expected_revision":"0",`, "", 1), 400},
		{"counter-resets", strings.Replace(valid, `"scope":"user"`, `"scope":"user","scope":"user"`, 1), 400},
		{"scope-resets", valid, 400}, {"pair-resets", valid, 400}, {"counter-resets?reset=true", valid, 400},
		{"counter-resets", valid, 200},
	} {
		before := reader.calls
		request := httptest.NewRequest("POST", "/v1/xray/observability/instances/a/traffic/"+test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != test.want || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal(response.StatusCode, string(data))
		}
		if test.want == 400 && reader.calls != before {
			t.Fatal("rejected reset reached native effect")
		}
		if test.want == 200 && !strings.Contains(string(data), `"previous_bytes":"-9007199254740993"`) {
			t.Fatal(string(data))
		}
	}
}
