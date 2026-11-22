package natsapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	"github.com/nats-io/nats.go"
)

func TestNATSStrictShapeDeadlinesAndExactBytes(t *testing.T) {
	reader := &readerFixture{}
	service, _ := services.New([]services.Binding{{ID: "a", Reader: reader}}, statusFixture{})
	h := Handler{Service: service, Timeout: time.Second}
	valid := []byte(`{"instance_id":"a","scope":"inbound","name":" A>>>B ","direction":"downlink"}`)
	for _, deadline := range []time.Time{time.Now().Add(100 * time.Millisecond), time.Now().Add(time.Hour)} {
		got := h.Read(context.Background(), valid, nats.Header{"Papilio-Deadline": {deadline.Format(time.RFC3339Nano)}})
		if !strings.Contains(string(got), `"bytes":"-9007199254740993"`) || reader.remaining > time.Second {
			t.Fatalf("response=%s remaining=%v", got, reader.remaining)
		}
	}
	before := reader.calls
	for _, headers := range []nats.Header{{"Papilio-Deadline": {"invalid"}}, {"Papilio-Deadline": {"a", "b"}}, {"Papilio-Deadline": {time.Now().Add(-time.Second).Format(time.RFC3339Nano)}}} {
		got := h.Read(context.Background(), valid, headers)
		if !strings.Contains(string(got), `"error"`) || reader.calls != before {
			t.Fatalf("invalid deadline dispatched %s", got)
		}
	}
	for _, data := range [][]byte{[]byte(`{}`), []byte(`{"instance_id":"a","scope":"user","name":"a","direction":"uplink","reset":true}`)} {
		if got := h.Read(context.Background(), data, nil); !strings.Contains(string(got), "invalid_argument") || reader.calls != before {
			t.Fatalf("bad shape dispatched %s", got)
		}
	}
	for _, test := range []struct {
		cause error
		code  string
	}{{domain.ErrCounterNotFound, "counter_not_found"}, {domain.ErrCapacity, "resource_exhausted"}, {domain.ErrExecutionChanged, "execution_changed"}, {errors.Join(domain.ErrExecutionChanged, domain.ErrCounterNotFound), "execution_changed"}, {errors.New("private endpoint"), "unavailable"}} {
		reader.err = test.cause
		got := h.Read(context.Background(), valid, nil)
		if !strings.Contains(string(got), test.code) || strings.Contains(string(got), "private") || strings.Contains(string(got), `"bytes"`) {
			t.Fatalf("failure=%s", got)
		}
	}
}
