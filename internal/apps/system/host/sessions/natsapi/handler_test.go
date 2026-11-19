package natsapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/natsapi"
	"github.com/nats-io/nats.go"
)

func TestSessionNATSSelectionPrecisionAndBoundaries(t *testing.T) {
	logind, records := &logindFixture{}, &recordsFixture{}
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{Logind: logind, Records: records}}
	page := string(handler.List(context.Background(), []byte(`{"offset":1,"limit":2}`), nil))
	if !strings.Contains(page, `"leader_pid":0`) || !strings.Contains(page, `"idle":false`) || logind.selection.Offset != 1 || logind.selection.Limit != 2 {
		t.Fatal(page)
	}
	read := string(handler.Read(context.Background(), []byte(`{"id":"s1"}`), nil))
	if !strings.Contains(read, `"started_unix_us":"9007199254740993"`) || !strings.Contains(read, `"remote":false`) || !strings.Contains(read, `"remote_host":null`) || logind.id != "s1" {
		t.Fatal(read)
	}
	legacy := string(handler.ListRecords(context.Background(), []byte(`{"offset":1,"limit":2}`), nil))
	if strings.Count(legacy, `"record_id":"p1"`) != 2 || !strings.Contains(legacy, `"started_unix_us":"-500000"`) || records.selection.Offset != 1 {
		t.Fatal(legacy)
	}
	before := logind.calls + records.calls
	for _, operation := range []struct {
		run     func(context.Context, []byte, nats.Header) []byte
		invalid []string
	}{{handler.List, []string{`null`, `[]`, `{"offset":null}`, `{"offset":16385}`, `{"limit":257}`, `{"unknown":1}`}}, {handler.ListRecords, []string{`{"offset":-1}`, `{"limit":null}`}}, {handler.Read, []string{`{}`, `{"id":null}`, `{"id":1}`, `{"id":""}`, `{"id":"s1","unknown":1}`}}} {
		for _, input := range operation.invalid {
			if result := string(operation.run(context.Background(), []byte(input), nil)); result != `{"error":"invalid_argument"}` {
				t.Fatalf("%s: %s", input, result)
			}
		}
	}
	if logind.calls+records.calls != before {
		t.Fatal("invalid input reached native readers")
	}
	if result := string(handler.List(context.Background(), []byte(strings.Repeat(" ", 2049)), nil)); result != `{"error":"resource_exhausted"}` {
		t.Fatal(result)
	}
	for _, failure := range []struct {
		cause error
		code  string
	}{{domain.ErrNotFound, "not_found"}, {domain.ErrPermissionDenied, "permission_denied"}, {domain.ErrUnsupported, "unimplemented"}, {domain.ErrResourceExhausted, "resource_exhausted"}, {context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline_exceeded"}, {errors.New("private source"), "unavailable"}} {
		logind.cause = failure.cause
		if result := string(handler.Read(context.Background(), []byte(`{"id":"s1"}`), nil)); result != `{"error":"`+failure.code+`"}` {
			t.Fatal(result)
		}
	}
}
