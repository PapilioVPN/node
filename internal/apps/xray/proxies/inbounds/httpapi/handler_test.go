package httpapi_test

import (
	"bytes"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestMalformedRequestsAreRejectedBeforeService(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{}.Register(app)
	for _, tc := range []struct{ method, path, body, contentType string }{
		{"GET", "inbounds?unknown=1", "", ""}, {"GET", "inbound-tags", "body", ""}, {"GET", "inbounds/control-status?tag=a", "", ""},
		{"POST", "inbounds", "{}", "application/json"}, {"POST", "inbounds?tag=a", "protobuf", "application/x-protobuf"},
		{"DELETE", "inbounds", "", ""}, {"DELETE", "inbounds?tag=a&tag=b", "", ""}, {"DELETE", "inbounds?tag=a", "body", ""},
	} {
		request := httptest.NewRequest(tc.method, "/v1/xray/proxies/instances/edge-a/"+tc.path, bytes.NewBufferString(tc.body))
		request.Header.Set("Content-Type", tc.contentType)
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, response.StatusCode, data)
		}
	}
}
