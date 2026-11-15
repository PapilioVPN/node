//go:build integration

package natsapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/modules/system/info/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsserver"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

type infoReader func(context.Context) (domain.NodeInfo, error)

func (f infoReader) GetNodeInfo(ctx context.Context) (domain.NodeInfo, error) { return f(ctx) }

func TestRequestShapeAndSanitizedOperationErrors(t *testing.T) {
	broker := testutil.NewNATSBroker(t)
	cfg := broker.Config()
	server, err := natsserver.NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	entries := []struct {
		name, body, code string
		cause            error
		calls            atomic.Int32
	}{
		{name: "valid", body: "{}"},
		{name: "null", body: "null", code: "invalid_argument"},
		{name: "empty", body: "", code: "invalid_argument"},
		{name: "unknown", body: `{"unknown":true}`, code: "invalid_argument"},
		{name: "trailing", body: "{} {}", code: "invalid_argument"},
		{name: "array", body: "[]", code: "invalid_argument"},
		{name: "canceled", body: "{}", cause: context.Canceled, code: "canceled"},
		{name: "deadline", body: "{}", cause: context.DeadlineExceeded, code: "deadline_exceeded"},
		{name: "failure", body: "{}", cause: errors.New("private-native-diagnostics"), code: "internal"},
	}
	for i := range entries {
		entry := &entries[i]
		handler := natsapi.Handler{MaxResponseBytes: cfg.MaxResponseBytes, Info: infoReader(func(context.Context) (domain.NodeInfo, error) {
			entry.calls.Add(1)
			return domain.NodeInfo{NodeID: "owned", Version: "version", APIVersion: "v1", Capabilities: []domain.Capability{domain.ReadNodeInfo}}, entry.cause
		})}
		if err := server.Handle(entry.name, handler.GetNodeInfo); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	client := broker.Client(t, "controller")
	for i := range entries {
		entry := &entries[i]
		t.Run(entry.name, func(t *testing.T) {
			message, err := client.RequestWithContext(ctx, cfg.SubjectPrefix+"."+entry.name, []byte(entry.body))
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				NodeID string `json:"node_id"`
				Error  *struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(message.Data, &response); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(message.Data), "private-native") {
				t.Fatal("native diagnostics exposed")
			}
			if entry.code == "" {
				if response.NodeID != "owned" || response.Error != nil {
					t.Fatalf("incorrect typed response: %s", message.Data)
				}
			} else if response.Error == nil || response.Error.Code != entry.code {
				t.Fatalf("wrong protocol error: %s", message.Data)
			}
			want := int32(1)
			if entry.code == "invalid_argument" {
				want = 0
			}
			if entry.calls.Load() != want {
				t.Fatal("invalid shape reached application or operation was duplicated")
			}
		})
	}
}
