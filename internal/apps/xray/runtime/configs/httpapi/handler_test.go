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

type builderFixture struct {
	calls int
	err   error
}

func (b *builderFixture) Validate(_ context.Context, c domain.Candidate) (domain.Validation, error) {
	b.calls++
	return domain.Validation{Valid: string(c.Content) == `{}`, Failure: errors.New("private native credential")}, b.err
}

func TestRawCandidateInputAndSanitizedResponses(t *testing.T) {
	b := &builderFixture{}
	service, _ := services.New([]services.Binding{{ID: "a", Builder: b}})
	app := fiber.New()
	(Handler{Service: service, Timeout: time.Second}).Register(app)
	for _, test := range []struct {
		query, body string
		status      int
	}{
		{"format=json", `{}`, 200}, {"format=json", `invalid`, 200}, {"", `{}`, 400}, {"format=json&format=yaml", `{}`, 400}, {"format=json&extra=1", `{}`, 400}, {"format=unknown", `{}`, 400}, {"format=json", "", 400},
	} {
		response, err := app.Test(httptest.NewRequest("POST", "/v1/xray/runtime/instances/a/config-validations?"+test.query, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != test.status || strings.Contains(string(data), "private") || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d body=%s", response.StatusCode, data)
		}
	}
	if b.calls != 2 {
		t.Fatalf("invalid input reached native builder: %d", b.calls)
	}
	b.err = errors.New("private source path")
	response, _ := app.Test(httptest.NewRequest("POST", "/v1/xray/runtime/instances/a/config-validations?format=json", strings.NewReader(`{}`)))
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != 503 || strings.Contains(string(data), "private") {
		t.Fatalf("native error leaked: %s", data)
	}
}
