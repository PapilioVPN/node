package natsapi_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/natsapi"
	"github.com/nats-io/nats.go"
)

func TestSensorInputDeadlineAndExactValues(t *testing.T) {
	r := &reader{}
	h := natsapi.Handler{Service: services.Service{Reader: r}, Timeout: time.Second}
	ctx := context.Background()
	if data := h.Temperatures(ctx, []byte("{}"), nil); !strings.Contains(string(data), `"milli_celsius":"-12500"`) {
		t.Fatal(string(data))
	}
	if data := h.Fans(ctx, []byte("{}"), nil); !strings.Contains(string(data), `"rpm":"9007199254740993"`) {
		t.Fatal(string(data))
	}
	if data := h.PowerSupplies(ctx, []byte("{}"), nil); !strings.Contains(string(data), `"present":false`) {
		t.Fatal(string(data))
	}
	before := r.calls
	for _, invalid := range []string{"null", "[]", `{"extra":1}`, strings.Repeat(" ", 1025)} {
		data := h.Fans(ctx, []byte(invalid), nil)
		if !strings.Contains(string(data), `"error":`) {
			t.Fatal(string(data))
		}
	}
	expired := nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}}
	if data := h.Fans(ctx, []byte("{}"), expired); string(data) != `{"error":"deadline_exceeded"}` || r.calls != before {
		t.Fatalf("%s calls%d", data, r.calls)
	}
}
