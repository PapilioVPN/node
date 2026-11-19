package natsapi_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/natsapi"
	"github.com/nats-io/nats.go"
)

func TestNodeNATSPrecisionInputAndDeadline(t *testing.T) {
	reader := &reader{}
	handler := natsapi.Handler{Service: services.Service{Reader: reader, Probes: reader}, Timeout: time.Second}
	ctx := context.Background()
	if data := handler.Info(ctx, []byte("{}"), nil); !strings.Contains(string(data), `"modified":false`) {
		t.Fatal(string(data))
	}
	if data := handler.Resources(ctx, []byte("{}"), nil); !strings.Contains(string(data), `"start_ticks":"9007199254740993"`) {
		t.Fatal(string(data))
	}
	if data := handler.Diagnostics(ctx, []byte("{}"), nil); !strings.Contains(string(data), `"round_trip_ns":null`) || strings.Contains(string(data), "private native cause") {
		t.Fatal(string(data))
	}
	before := reader.calls
	for _, data := range []string{"null", "[]", `{"extra":1}`, strings.Repeat(" ", 1025)} {
		if result := handler.Info(ctx, []byte(data), nil); !strings.Contains(string(result), `"error":`) {
			t.Fatal(string(result))
		}
	}
	headers := nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}}
	if data := handler.Resources(ctx, []byte("{}"), headers); string(data) != `{"error":"deadline_exceeded"}` || reader.calls != before {
		t.Fatal(string(data))
	}
}
