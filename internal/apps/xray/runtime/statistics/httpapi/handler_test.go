package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/domain"
	"github.com/gofiber/fiber/v3"
)

type readerFixture struct {
	calls int
	err   error
}

func (r *readerFixture) Read(context.Context) (domain.Statistics, error) {
	r.calls++
	return domain.Statistics{Goroutines: 5}, r.err
}

type statusFixture struct{}

func (statusFixture) Status(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 1}}, nil
}
func TestRuntimeHTTPInputAndPrivateFailureMapping(t *testing.T) {
	reader := &readerFixture{}
	service, _ := services.New([]services.Binding{{ID: "a", Reader: reader}, {ID: "disabled"}}, statusFixture{})
	app := fiber.New()
	(Handler{Service: service, Timeout: time.Second}).Register(app)
	for _, test := range []struct {
		path   string
		cause  error
		status int
	}{{"a", nil, 200}, {"a?unknown=1", nil, 400}, {"missing", nil, 404}, {"disabled", nil, 409}, {"a", domain.ErrExecutionChanged, 409}, {"a", domain.ErrCapacity, 429}, {"a", context.DeadlineExceeded, 504}, {"a", errors.New("private credential path"), 503}} {
		reader.err = test.cause
		path := "/v1/xray/runtime/instances/" + test.path + "/statistics"
		if strings.Contains(test.path, "?") {
			path = "/v1/xray/runtime/instances/a/statistics?unknown=1"
		}
		response, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != test.status || response.Header.Get("Cache-Control") != "no-store" || strings.Contains(string(data), "private") {
			t.Fatalf("HTTP status=%d body=%s", response.StatusCode, data)
		}
		if test.cause != nil && strings.Contains(string(data), `"goroutines"`) {
			t.Fatal("failed read exposed successful-looking zero fields")
		}
	}
	if reader.calls != 5 {
		t.Fatalf("invalid HTTP input reached native API: %d", reader.calls)
	}
}
