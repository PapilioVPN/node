package natsapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/domain"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/natsapi"
	"github.com/nats-io/nats.go"
)

func TestFilesystemNATSSelectionPrecisionAndBoundaries(t *testing.T) {
	reader := &readerFixture{}
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader}}
	if result := string(handler.List(context.Background(), []byte(`{"after_id":41,"limit":1}`), nil)); reader.selection.AfterID != 41 || reader.selection.Limit != 1 || !strings.Contains(result, `"next_after_id":42`) || !strings.Contains(result, `"read_only":true`) {
		t.Fatal(result)
	}
	if result := string(handler.Usage(context.Background(), []byte(`{"path":"/path with spaces"}`), nil)); reader.path != "/path with spaces" || !strings.Contains(result, `"total_bytes":"9007199254740993"`) {
		t.Fatal(result)
	}
	before := reader.calls
	for _, operation := range []struct {
		run     func(context.Context, []byte, nats.Header) []byte
		invalid []string
	}{
		{handler.List, []string{`null`, `[]`, `{"after_id":-1}`, `{"after_id":null}`, `{"limit":257}`, `{"unknown":1}`}}, {handler.Usage, []string{`{}`, `{"path":null}`, `{"path":42}`, `{"path":"relative"}`, `{"path":"/","unknown":1}`}},
	} {
		for _, input := range operation.invalid {
			if result := string(operation.run(context.Background(), []byte(input), nil)); result != `{"error":"invalid_argument"}` {
				t.Fatalf("%s: %s", input, result)
			}
		}
		if result := string(operation.run(context.Background(), []byte(strings.Repeat(" ", 32769)), nil)); result != `{"error":"resource_exhausted"}` {
			t.Fatal(result)
		}
	}
	if reader.calls != before {
		t.Fatal("invalid input reached native I/O")
	}
	for _, failure := range []struct {
		cause error
		code  string
	}{{domain.ErrNotFound, "not_found"}, {domain.ErrPermissionDenied, "permission_denied"}, {domain.ErrResourceExhausted, "resource_exhausted"}, {context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline_exceeded"}, {errors.New("private path"), "unavailable"}} {
		reader.cause = failure.cause
		if result := string(handler.Usage(context.Background(), []byte(`{"path":"/"}`), nil)); result != `{"error":"`+failure.code+`"}` {
			t.Fatal(result)
		}
	}
	reader.cause = nil
	if result := string(handler.Usage(context.Background(), []byte(`{"path":"/"}`), nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}})); result != `{"error":"deadline_exceeded"}` {
		t.Fatal(result)
	}
	longPath := "/" + strings.Repeat("a", 4094)
	if result := string(handler.Usage(context.Background(), []byte(`{"path":"`+longPath+`"}`), nil)); strings.Contains(result, `"error"`) || reader.path != longPath {
		t.Fatal("NATS rejected a supported path length")
	}
}
