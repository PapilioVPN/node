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

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/httpapi"
	"github.com/gofiber/fiber/v3"
)

func TestCPUFrequencyRoutePreservesUnitsPresenceAndIssues(t *testing.T) {
	app := fiber.New()
	calls := 0
	httpapi.Handler{Timeout: time.Second, Service: services.Service{FrequencyReader: frequencyReaderFunc(func(ctx context.Context) (domain.Frequencies, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded frequency read")
		}
		calls++
		return sampleFrequencies(), nil
	})}}.Register(app)
	response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/cpu/frequency", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var got struct {
		Present  bool `json:"interface_present"`
		Policies []struct {
			Current     string                               `json:"scaling_current_khz"`
			Min         *string                              `json:"hardware_min_khz"`
			Hardware    *string                              `json:"hardware_current_khz"`
			Available   []string                             `json:"available_khz"`
			Related     []uint32                             `json:"related_cpus"`
			Unavailable []struct{ Attribute, Reason string } `json:"unavailable"`
		} `json:"policies"`
	}
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" || calls != 1 || !got.Present || len(got.Policies) != 1 {
		t.Fatalf("frequency response: %+v; calls=%d", got, calls)
	}
	row := got.Policies[0]
	if row.Current != "9007199254740993" || row.Min == nil || *row.Min != "0" || row.Hardware != nil || len(row.Available) != 1 || row.Available[0] != "9007199254740993" || len(row.Related) != 2 || row.Related[1] != 3 || len(row.Unavailable) != 1 || row.Unavailable[0].Attribute != "cpuinfo_cur_freq" || row.Unavailable[0].Reason != "permission_denied" {
		t.Fatalf("lost precise frequencies/presence: %+v", row)
	}
}

func TestHTTPFrequencyRejectsInputAndSanitizesErrors(t *testing.T) {
	app := fiber.New()
	httpapi.Handler{}.Register(app)
	for _, test := range []struct {
		method, path, body string
		code               int
	}{{"GET", "?x=1", "", 400}, {"GET", "", "{}", 400}, {"POST", "", "", 405}} {
		response, err := app.Test(httptest.NewRequest(test.method, "/v1/system/host/cpu/frequency"+test.path, strings.NewReader(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.code {
			t.Fatal(response.StatusCode)
		}
	}
	for _, test := range []struct {
		cause  error
		code   string
		status int
	}{{errors.New("private sysfs path"), "unavailable", 503}, {context.Canceled, "canceled", 503}, {context.DeadlineExceeded, "deadline_exceeded", 504}} {
		app := fiber.New()
		httpapi.Handler{Timeout: time.Second, Service: services.Service{FrequencyReader: frequencyReaderFunc(func(context.Context) (domain.Frequencies, error) { return sampleFrequencies(), test.cause })}}.Register(app)
		response, err := app.Test(httptest.NewRequest("GET", "/v1/system/host/cpu/frequency", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != test.status || string(body) != `{"error":"`+test.code+`"}` {
			t.Fatalf("unsafe failure: %s %v", body, err)
		}
	}
}
