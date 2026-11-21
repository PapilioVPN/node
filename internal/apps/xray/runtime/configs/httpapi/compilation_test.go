package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/gofiber/fiber/v3"
)

type compilerFixture struct {
	calls int
	err   error
}

func (c *compilerFixture) Compile(context.Context, []domain.Source) (domain.Compilation, error) {
	c.calls++
	return domain.Compilation{Content: []byte{1, 2, 3}, SHA256: "digest"}, c.err
}

func TestHTTPCompilationInputAndNativeFailureMapping(t *testing.T) {
	compiler := &compilerFixture{}
	service, _ := services.New([]services.Binding{{ID: "a", Compiler: compiler}})
	app := fiber.New()
	(Handler{Service: service, Timeout: time.Second}).Register(app)
	const input = `{"instance_id":"a","sources":[{"name":"base.json","content":"e30="}]}`
	for _, test := range []struct {
		query, body string
		err         error
		status      int
	}{
		{"", input, nil, 200}, {"?unknown=1", input, nil, 400}, {"", `{"instance_id":"a","sources":null}`, nil, 400},
		{"", input, domain.ErrRejected, 422}, {"", input, errors.New("private compilation credential"), 503},
	} {
		compiler.err = test.err
		response, err := app.Test(httptest.NewRequest("POST", "/v1/xray/runtime/config-compilations"+test.query, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != test.status || response.Header.Get("Cache-Control") != "no-store" || strings.Contains(string(data), "private") {
			t.Fatalf("HTTP compilation status=%d body=%s", response.StatusCode, data)
		}
		if test.err != nil && strings.Contains(string(data), `"content"`) {
			t.Fatal("failed compilation returned partial artifact")
		}
	}
	if compiler.calls != 3 {
		t.Fatal("invalid HTTP input dispatched a native compilation")
	}
}
