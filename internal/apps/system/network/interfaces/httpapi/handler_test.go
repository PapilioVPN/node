package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestHTTPInterfaceInventoryPreservesAddressesAndCounterPrecision(t *testing.T) {
	reader := &readerFixture{}
	app := fiber.New()
	httpapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader}}.Register(app)
	for _, path := range []string{"", "/counters"} {
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/network/interfaces"+path, nil))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("response: %s %v", body, err)
		}
		if path == "" {
			var got struct {
				Interfaces []struct {
					Index     uint32
					Flags     uint32
					Addresses []struct {
						Local string  `json:"local_prefix"`
						Peer  *string `json:"peer_prefix"`
						Valid uint32  `json:"valid_lifetime_seconds"`
					}
				}
				Unmatched []struct {
					Index uint32 `json:"interface_index"`
				} `json:"unmatched_addresses"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Interfaces) != 1 || got.Interfaces[0].Index != 42 || got.Interfaces[0].Flags != 0x80000001 || len(got.Interfaces[0].Addresses) != 1 || got.Interfaces[0].Addresses[0].Local != "192.0.2.4/32" || got.Interfaces[0].Addresses[0].Peer == nil || *got.Interfaces[0].Addresses[0].Peer != "198.51.100.5/24" || got.Interfaces[0].Addresses[0].Valid != 1<<32-1 || len(got.Unmatched) != 1 || got.Unmatched[0].Index != 77 {
				t.Fatalf("lost interface facts: %s", body)
			}
		} else {
			var got struct {
				Interfaces []struct{ Counters *map[string]string }
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Interfaces) != 2 || got.Interfaces[0].Counters == nil || (*got.Interfaces[0].Counters)["receive_bytes"] != "9007199254740993" || (*got.Interfaces[0].Counters)["transmit_bytes"] != "18446744073709551615" || (*got.Interfaces[0].Counters)["transmit_carrier_errors"] != "11" || got.Interfaces[1].Counters != nil {
				t.Fatalf("lost counter precision/absence: %s", body)
			}
		}
	}
	if reader.calls != 1 || reader.counterCalls != 1 {
		t.Fatal("unfocused reads")
	}
}

func TestHTTPInterfaceRoutesRejectShapeAndSanitizeErrors(t *testing.T) {
	for _, path := range []string{"", "/counters"} {
		app := fiber.New()
		httpapi.Handler{}.Register(app)
		for _, input := range []struct {
			method, query, body string
			status              int
		}{{"GET", "?unknown=1", "", 400}, {"GET", "", "{}", 400}, {"POST", "", "", 405}} {
			response, err := app.Test(httptest.NewRequest(input.method, "/v1/system/network/interfaces"+path+input.query, strings.NewReader(input.body)))
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != input.status {
				t.Fatal(response.StatusCode)
			}
		}
		for _, failure := range []struct {
			cause  error
			code   string
			status int
		}{{errors.New("private socket"), "unavailable", 503}, {context.Canceled, "canceled", 503}, {context.DeadlineExceeded, "deadline_exceeded", 504}} {
			app := fiber.New()
			httpapi.Handler{Timeout: time.Second, Service: services.Service{Reader: &readerFixture{cause: failure.cause}}}.Register(app)
			response, err := app.Test(httptest.NewRequest("GET", "/v1/system/network/interfaces"+path, nil))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != failure.status || string(body) != `{"error":"`+failure.code+`"}` {
				t.Fatalf("unsafe failure: %s", body)
			}
		}
	}
}
