package httpapi

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/app/services"
	"github.com/gofiber/fiber/v3"
)

func TestRoutingShapeAndAbsence(t *testing.T) {
	r := &reader{}
	s, err := services.New([]services.Binding{{ID: "edge", Reader: r}, {ID: "disabled"}}, running{})
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	(Handler{Service: s, Timeout: time.Second}).Register(app)
	root := "/v1/xray/observability/instances/edge/outbound-health/"
	for _, tt := range []struct {
		path     string
		status   int
		contains string
	}{
		{root + "status?outbound_tag=", 200, `"status":{"outbound_tag":""`},
		{root + "status?outbound_tag=absent", 200, `"status":null`},
		{root + "statuses", 200, `"statuses":[`},
		{root + "status", 400, `"error":"invalid_argument"`},
		{root + "status?outbound_tag=a&outbound_tag=b", 400, `invalid_argument`},
		{root + "status?outbound_tag=a&unexpected=1", 400, `invalid_argument`},
		{root + "status?outbound_tag=%ff", 400, `invalid_argument`},
		{root + "statuses?page_size=51", 400, `invalid_argument`},
		{root + "statuses?page_size=4294967296", 400, `invalid_argument`},
		{root + "statuses?outbound_tag=a", 400, `invalid_argument`},
		{"/v1/xray/observability/instances/disabled/outbound-health/status?outbound_tag=", 409, `api_not_configured`},
	} {
		response, err := app.Test(httptest.NewRequest("GET", tt.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != tt.status || !strings.Contains(string(data), tt.contains) || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal(tt, response.StatusCode, string(data))
		}
	}
	if r.calls != 3 {
		t.Fatal("invalid requests performed I/O", r.calls)
	}
	response, err := app.Test(httptest.NewRequest("GET", root+"status?outbound_tag=", strings.NewReader("{}")))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal(response.Status)
	}
	response, err = app.Test(httptest.NewRequest("POST", root+"statuses", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 405 {
		t.Fatal(response.Status)
	}
}
