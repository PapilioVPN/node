package natsapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/domain"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/natsapi"
	"github.com/nats-io/nats.go"
)

func TestDeviceNATSSelectionPrecisionAndBoundaries(t *testing.T) {
	reader := &readerFixture{}
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader}}
	result := string(handler.List(context.Background(), []byte(`{"after_name":"nvme0n1","limit":2}`), nil))
	if reader.selection.AfterName != "nvme0n1" || reader.selection.Limit != 2 {
		t.Fatal("lost selection")
	}
	for _, expected := range []string{`"read_sectors":"9007199254740993"`, `"write_sectors":"18446744073709551615"`, `"discard":null`, `"discard":{"completed":"0"`, `"next_after_name":"sdb"`, `"sector_size_bytes":512`} {
		if !strings.Contains(result, expected) {
			t.Fatalf("lost %s: %s", expected, result)
		}
	}
	before := reader.calls
	for _, input := range []string{`null`, `[]`, `{"after_name":null}`, `{"after_name":2}`, `{"limit":null}`, `{"limit":257}`, `{"unknown":1}`} {
		if result := string(handler.List(context.Background(), []byte(input), nil)); result != `{"error":"invalid_argument"}` {
			t.Fatal(result)
		}
	}
	if reader.calls != before {
		t.Fatal("invalid selection reached native read")
	}
	if result := string(handler.List(context.Background(), []byte(strings.Repeat(" ", 2049)), nil)); result != `{"error":"resource_exhausted"}` {
		t.Fatal(result)
	}
	for _, failure := range []struct {
		cause error
		code  string
	}{{domain.ErrResourceExhausted, "resource_exhausted"}, {context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline_exceeded"}, {errors.New("private path"), "unavailable"}} {
		reader.cause = failure.cause
		if result := string(handler.List(context.Background(), []byte(`{}`), nil)); result != `{"error":"`+failure.code+`"}` {
			t.Fatal(result)
		}
	}
	reader.cause = nil
	if result := string(handler.List(context.Background(), []byte(`{}`), nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}})); result != `{"error":"deadline_exceeded"}` {
		t.Fatal(result)
	}
}
