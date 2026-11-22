package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	"github.com/gofiber/fiber/v3"
)

func TestHTTPSelectionBoundsErrorsAndExactBytes(t *testing.T) {
	reader := &readerFixture{}
	service, _ := services.New([]services.Binding{{ID: "a", Reader: reader}, {ID: "disabled"}}, statusFixture{})
	app := fiber.New(fiber.Config{ReadBufferSize: 64 << 10})
	(Handler{Service: service, Timeout: time.Second}).Register(app)
	query := "?scope=user&name=+A%3E%3E%3EB%2F+&direction=uplink"
	for _, test := range []struct {
		id, query, method, body string
		cause                   error
		want                    int
		dispatched              bool
	}{
		{"a", query, "GET", "", nil, 200, true},
		{"a", query + "&reset=true", "GET", "", nil, 400, false},
		{"a", query + "&name=x", "GET", "", nil, 400, false},
		{"a", "?scope=user&name=x", "GET", "", nil, 400, false},
		{"a", "?scope=invalid&name=x&direction=uplink", "GET", "", nil, 400, false},
		{"a", query, "GET", "{}", nil, 400, false},
		{"a", query, "POST", "", nil, 405, false},
		{"missing", query, "GET", "", nil, 404, false},
		{"disabled", query, "GET", "", nil, 409, false},
		{"a", query, "GET", "", domain.ErrCounterNotFound, 404, true},
		{"a", query, "GET", "", domain.ErrCapacity, 429, true},
		{"a", query, "GET", "", domain.ErrExecutionChanged, 409, true},
		{"a", query, "GET", "", errors.Join(domain.ErrExecutionChanged, domain.ErrCounterNotFound), 409, true},
		{"a", query, "GET", "", context.DeadlineExceeded, 504, true},
		{"a", query, "GET", "", errors.New("private credential"), 503, true},
	} {
		before := reader.calls
		reader.err = test.cause
		response, err := app.Test(httptest.NewRequest(test.method, "/v1/xray/observability/instances/"+test.id+"/traffic/counter"+test.query, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != test.want || strings.Contains(string(data), "private") {
			t.Fatalf("status=%d want=%d body=%s", response.StatusCode, test.want, data)
		}
		if test.method == "GET" && response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable observation")
		}
		wantCalls := 0
		if test.dispatched {
			wantCalls = 1
		}
		if reader.calls-before != wantCalls {
			t.Fatal("wrong dispatch count", reader.calls-before)
		}
		if test.want == 200 && (!strings.Contains(string(data), `"bytes":"-9007199254740993"`) || reader.selection.Name != " A>>>B/ ") {
			t.Fatalf("lossy result %s %+v", data, reader.selection)
		}
		if test.cause != nil && strings.Contains(string(data), `"bytes"`) {
			t.Fatal("failure returned successful-looking zero")
		}
	}
}
