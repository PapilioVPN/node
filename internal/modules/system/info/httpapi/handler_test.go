package httpapi_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/modules/system/info/httpapi"
	"github.com/gofiber/fiber/v3"
)

type infoReader func(context.Context) (domain.NodeInfo, error)

func (f infoReader) GetNodeInfo(ctx context.Context) (domain.NodeInfo, error) { return f(ctx) }

func TestUnverifiedClientDoesNotReachApplication(t *testing.T) {
	calls := 0
	app := fiber.New()
	httpapi.Handler{Info: infoReader(func(context.Context) (domain.NodeInfo, error) { calls++; return domain.NodeInfo{}, nil })}.Register(app)
	response, err := app.Test(httptest.NewRequest("GET", "/v1/node", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusUnauthorized || calls != 0 {
		t.Fatal("unverified client reached application")
	}
}
