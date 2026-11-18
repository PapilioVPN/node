package natsapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/processes/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/natsapi"
	"github.com/nats-io/nats.go"
)

func TestProcessNATSPreservesPrecisionSelectionAndPresence(t *testing.T) {
	reader := &readerFixture{}
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader, TicksPerSecond: 100}}
	if result := string(handler.Count(context.Background(), []byte(`{}`), nil)); !strings.Contains(result, `"count":"9007199254740993"`) {
		t.Fatal(result)
	}
	if result := string(handler.Read(context.Background(), []byte(`{"pid":42}`), nil)); !strings.Contains(result, `"user_ticks":"18446744073709551615"`) || !strings.Contains(result, `"start_ticks":"9007199254740993"`) || reader.pid != 42 {
		t.Fatal(result)
	}
	if result := string(handler.List(context.Background(), []byte(`{"after_pid":41,"limit":1}`), nil)); !strings.Contains(result, `"process":null,"issue":"permission_denied"`) || !strings.Contains(result, `"next_after_pid":43`) || reader.selection.AfterPID != 41 || reader.selection.Limit != 1 {
		t.Fatal(result)
	}
}

func TestProcessNATSValidatesShapesDeadlinesAndErrors(t *testing.T) {
	reader := &readerFixture{}
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader}}
	for _, operation := range []struct {
		run     func(context.Context, []byte, nats.Header) []byte
		invalid []string
	}{
		{handler.Count, []string{`null`, `[]`, `{"pid":42}`}}, {handler.Read, []string{`{}`, `{"pid":null}`, `{"pid":"42"}`, `{"pid":0}`, `{"pid":2147483648}`, `{"pid":42,"unknown":1}`}}, {handler.List, []string{`{"limit":null}`, `{"limit":257}`, `{"limit":-1}`, `{"after_pid":-1}`, `{"unknown":1}`}},
	} {
		for _, input := range operation.invalid {
			if result := string(operation.run(context.Background(), []byte(input), nil)); result != `{"error":"invalid_argument"}` {
				t.Fatalf("%s: %s", input, result)
			}
		}
		if result := string(operation.run(context.Background(), []byte(strings.Repeat(" ", 1025)), nil)); result != `{"error":"resource_exhausted"}` {
			t.Fatal(result)
		}
	}
	if reader.calls != 0 {
		t.Fatal("invalid input reached I/O")
	}
	for _, headers := range []nats.Header{{"Papilio-Deadline": []string{"bad"}}, {"Papilio-Deadline": []string{time.Now().Format(time.RFC3339Nano), time.Now().Format(time.RFC3339Nano)}}} {
		if result := string(handler.Count(context.Background(), []byte(`{}`), headers)); result != `{"error":"invalid_argument"}` {
			t.Fatal(result)
		}
	}
	if result := string(handler.Count(context.Background(), []byte(`{}`), nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}})); result != `{"error":"deadline_exceeded"}` {
		t.Fatal(result)
	}
	for _, failure := range []struct {
		cause error
		code  string
	}{{domain.ErrNotFound, "not_found"}, {domain.ErrPermissionDenied, "permission_denied"}, {domain.ErrResourceExhausted, "resource_exhausted"}, {context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline_exceeded"}, {errors.New("private path"), "unavailable"}} {
		reader.cause = failure.cause
		if result := string(handler.Read(context.Background(), []byte(`{"pid":42}`), nil)); result != `{"error":"`+failure.code+`"}` {
			t.Fatal(result)
		}
	}
}
