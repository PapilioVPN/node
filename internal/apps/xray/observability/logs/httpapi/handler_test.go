package httpapi_test

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/httpapi"
	"github.com/gofiber/fiber/v3"
)

type reader struct{}

func (reader) Read(_ context.Context, _ string, offset uint64, _ uint32) (domain.Chunk, error) {
	return domain.Chunk{ExecutionID: "run", Generation: 1, Offset: offset, NextOffset: offset + uint64(len("[Info] password=secret\n")), Data: []byte("[Info] password=secret\n"), ObservedAt: time.Now()}, nil
}
func TestHTTPBoundaries(t *testing.T) {
	s, err := services.New([]services.Binding{{ID: "edge", Reader: reader{}}})
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	httpapi.Handler{Service: s, Timeout: time.Second}.Register(app)
	for _, suffix := range []string{"?limit=51", "?limit=1&limit=2", "?cursor=a", "?unknown=true", "/updates?wait_ms=1", "/updates?cursor=bad&wait_ms=1001"} {
		r, err := app.Test(httptest.NewRequest("GET", "/v1/xray/observability/instances/edge/logs"+suffix, nil))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 400 || r.Header.Get("Cache-Control") != "no-store" {
			t.Fatal(suffix, r.StatusCode, string(data))
		}
	}
	r, err := app.Test(httptest.NewRequest("GET", "/v1/xray/observability/instances/edge/logs", nil))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || strings.Contains(string(data), "secret") || !strings.Contains(string(data), `"redacted":true`) {
		t.Fatal(r.StatusCode, string(data))
	}
}
