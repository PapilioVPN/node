package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/app/services"
	"github.com/gofiber/fiber/v3"
)

func TestCollectionRoutesPagingShapeAndNoRefetch(t *testing.T) {
	r := &reader{}
	s, _ := services.New([]services.Binding{{ID: "a", Reader: r, Collection: r}}, statusReader{})
	app := fiber.New()
	Handler{Service: s, Timeout: time.Second}.Register(app)
	for _, suffix := range []string{"users", "records"} {
		path := "/v1/xray/users/instances/a/online/" + suffix
		var first struct {
			Page struct {
				Next string `json:"next_page_token"`
			}
			Records []json.RawMessage
		}
		reply, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewDecoder(reply.Body).Decode(&first); err != nil {
			t.Fatal(err)
		}
		reply.Body.Close()
		if len(first.Records) != 50 || first.Page.Next == "" {
			t.Fatal(first)
		}
		reply, err = app.Test(httptest.NewRequest("GET", path+"?page_token="+url.QueryEscape(first.Page.Next), nil))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(reply.Body)
		reply.Body.Close()
		if reply.StatusCode != 200 || !strings.Contains(string(data), `"fresh":false`) || !strings.Contains(string(data), `email-50`) {
			t.Fatal(string(data))
		}
		for _, query := range []string{"page_size=51", "page_size=-1", "page_size=1.1", "page_size=1&page_size=2", "email=x", "inbound_tag=x"} {
			reply, err = app.Test(httptest.NewRequest("GET", path+"?"+query, nil))
			if err != nil {
				t.Fatal(err)
			}
			reply.Body.Close()
			if reply.StatusCode != 400 {
				t.Fatal(fmt.Sprint(query), reply.StatusCode)
			}
		}
	}
	if r.calls != 2 {
		t.Fatal("refetch", r.calls)
	}
}
