package natsapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/identity/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/identity/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/identity/natsapi"
	"github.com/nats-io/nats.go"
)

type readerFunc func(context.Context) (domain.Identity, error)

func (read readerFunc) Read(ctx context.Context) (domain.Identity, error) { return read(ctx) }

func TestReadRejectsUnpermittedAndInvalidRequestsBeforeService(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second}
	if got := string(handler.Read(context.Background(), []byte("invalid"), nil)); got != `{"error":"permission_denied"}` {
		t.Fatal(got)
	}
	handler.AllowRead = true
	for _, input := range []string{"", "null", "[]", `{"unknown":1}`, "{}{}", strings.Repeat(" ", 1025)} {
		result := handler.Read(context.Background(), []byte(input), nil)
		var response struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(result, &response); err != nil || response.Error == "" {
			t.Fatalf("invalid request accepted: %q %s", input, result)
		}
	}
	for _, values := range [][]string{{"invalid"}, {"invalid", "duplicate"}} {
		if got := string(handler.Read(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": values})); got != `{"error":"invalid_argument"}` {
			t.Fatal(got)
		}
	}
}

func TestCallerDeadlineOnlyShortensConfiguredTimeout(t *testing.T) {
	handler := natsapi.Handler{AllowRead: true, Timeout: 20 * time.Millisecond, Service: services.Service{Reader: readerFunc(func(ctx context.Context) (domain.Identity, error) {
		<-ctx.Done()
		return domain.Identity{}, ctx.Err()
	})}}
	for _, deadline := range []time.Time{time.Now().Add(-time.Second), time.Now().Add(time.Hour)} {
		got := handler.Read(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": {deadline.UTC().Format(time.RFC3339Nano)}})
		if string(got) != `{"error":"deadline_exceeded"}` {
			t.Fatal(string(got))
		}
	}
}
