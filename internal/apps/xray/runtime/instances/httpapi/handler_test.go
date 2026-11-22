package httpapi

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestRoutesStrictInputsEffectsAndPrivateErrors(t *testing.T) {
	h, r := fixture()
	app := fiber.New()
	h.Register(app)
	for _, test := range []struct {
		method, path, body string
		status             int
	}{{"GET", "/v1/core/engines/instances/a", "", 200}, {"GET", "/v1/core/engines/instances/a?unknown=1", "", 400}, {"PUT", "/v1/core/engines/instances/a/execution", "{}", 400}, {"PUT", "/v1/core/engines/instances/missing/execution", "", 404}, {"GET", "/v1/core/engines/instances/a/readiness", "", 200}} {
		response, err := app.Test(httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != test.status || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: %d %s", test.method, test.path, response.StatusCode, data)
		}
		if test.path == "/v1/core/engines/instances/a" && !strings.Contains(string(data), `"generation":"9007199254740993"`) {
			t.Fatalf("lost generation precision %s", data)
		}
	}
	if r.calls != 0 {
		t.Fatal("reads or rejected inputs mutated execution")
	}
	r.fail = true
	response, err := app.Test(httptest.NewRequest("POST", "/v1/core/engines/instances/a/executions", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != 503 || !strings.Contains(string(data), `"effect":"partial"`) || !strings.Contains(string(data), `"state":"stopped"`) || strings.Contains(string(data), "secret") {
		t.Fatalf("partial restart: %s", data)
	}
}

func TestRecoveryRoutesRejectMalformedRequestsBeforeEffects(t *testing.T) {
	h, r := fixture()
	app := fiber.New()
	h.Register(app)
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/v1/core/engines/instances", "", 200},
		{"GET", "/v1/core/engines/instances?limit=51", "", 400},
		{"GET", "/v1/core/engines/instances?limit=1&limit=2", "", 400},
		{"PUT", "/v1/core/engines/instances/a/retention", `{"execution_id":"one"}`, 400},
		{"POST", "/v1/core/engines/instances/a/repairs", `{"max_attempts":4}`, 400},
		{"POST", "/v1/core/engines/instances/a/leases", `{"expected_revision":"0","execution_id":"one","owner":"x","ttl_seconds":1,"policy":"stop","token":"secret"}`, 400},
		{"PUT", "/v1/core/engines/instances/a/lease", `{"expected_revision":"00","token":"secret","ttl_seconds":1}`, 400},
		{"DELETE", "/v1/core/engines/instances/a/lease", `{"expected_revision":"0","token":null}`, 400},
	} {
		response, err := app.Test(httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatalf("%s: %d %s", test.path, response.StatusCode, data)
		}
	}
	if r.calls != 0 {
		t.Fatal("invalid recovery input mutated engine")
	}
}
