package httpapi

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/gofiber/fiber/v3"
)

func TestStateHTTPRoutesRejectAmbiguousSelectionBeforeDispatch(t *testing.T) {
	app := fiber.New()
	(Handler{Timeout: time.Second}).Register(app)
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{"PUT", "/configuration", `{"sources":[]}`, 400},
		{"PUT", "/configuration", `{"expected_revision":"0"}`, 400},
		{"PUT", "/configuration", `{"instance_id":"b","expected_revision":"0","sources":[]}`, 400},
		{"POST", "/configuration/applications", `{"expected_revision":"1","sources":[]}`, 400},
		{"POST", "/configuration/applications", `{"expected_revision":null}`, 400},
		{"GET", "/configuration/status?unknown=1", ``, 400},
		{"GET", "/configuration", ``, 409},
	} {
		response, err := app.Test(httptest.NewRequest(test.method, "/v1/xray/runtime/instances/a"+test.path, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != test.status || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s status=%d %s", test.method, test.path, response.StatusCode, data)
		}
	}
}
func TestStateHTTPFailureKeepsPersistenceEvidence(t *testing.T) {
	app := fiber.New()
	app.Put("/instances/:id", func(request fiber.Ctx) error {
		return changeResult(request, domain.Change{Persistence: domain.Persistence{Published: true, Metadata: domain.DesiredMetadata{Revision: 7}}}, domain.ErrDurabilityUnknown)
	})
	response, err := app.Test(httptest.NewRequest("PUT", "/instances/a", nil))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 503 || !strings.Contains(string(data), `"published":true`) || !strings.Contains(string(data), `"revision":"7"`) || !strings.Contains(string(data), `"error":"durability_unknown"`) {
		t.Fatalf("lost persisted effects: %d %s", response.StatusCode, data)
	}
}
