package httpapi_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/ports"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/httpapi"
	"github.com/gofiber/fiber/v3"
)

type controlled struct {
	ports.Operations
	calls int
}

func (c *controlled) Apply(context.Context, string, string, domain.Revision) (domain.ReceiptPage, error) {
	c.calls++
	return domain.ReceiptPage{Receipt: domain.Receipt{InstanceID: "one", OperationID: "op", Effect: accounts.Unknown, RequiresRecovery: true}}, errors.New("private credential diagnostic")
}
func TestRoutingShapeNoStoreAndFormedFailure(t *testing.T) {
	c := &controlled{}
	app := fiber.New()
	httpapi.Handler{Service: c, Timeout: time.Second}.Register(app)
	for _, fixture := range []struct{ method, path, body string }{{"GET", "/v1/xray/users/instances/one/sync/revision?unexpected=1", ""}, {"POST", "/v1/xray/users/instances/one/sync/applications", `{"plan_id":"p","plan_id":"q"}`}, {"POST", "/v1/xray/users/instances/one/sync/applications", `{"plan_id":"p"}`}, {"POST", "/v1/xray/users/instances/one/sync/revision", ""}} {
		r := httptest.NewRequest(fixture.method, fixture.path, strings.NewReader(fixture.body))
		r.Header.Set("Content-Type", "application/json")
		response, err := app.Test(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode < 400 || c.calls != 0 {
			t.Fatal(response.StatusCode, c.calls)
		}
	}
	r := httptest.NewRequest("POST", "/v1/xray/users/instances/one/sync/applications", strings.NewReader(`{"plan_id":"p","expected":{"epoch":"epoch"}}`))
	r.Header.Set("Content-Type", "application/json")
	response, err := app.Test(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" || !strings.Contains(string(body), `"effect":"unknown"`) || strings.Contains(string(body), "private") {
		t.Fatal(response.StatusCode, string(body), err)
	}
}
