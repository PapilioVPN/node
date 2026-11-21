package natsapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/nats-io/nats.go"
)

type compilerFixture struct {
	calls int
	err   error
}

func (c *compilerFixture) Compile(context.Context, []domain.Source) (domain.Compilation, error) {
	c.calls++
	return domain.Compilation{Content: []byte{1, 2, 3}, SHA256: "digest"}, c.err
}

func TestNATSCompilationDeadlineAndNativeFailure(t *testing.T) {
	compiler := &compilerFixture{}
	service, _ := services.New([]services.Binding{{ID: "a", Compiler: compiler}})
	h := Handler{Service: service, Timeout: time.Second}
	const input = `{"instance_id":"a","sources":[{"name":"base.json","content":"e30="}]}`
	response := h.Compile(context.Background(), []byte(input), nil)
	if !strings.Contains(string(response), `"content":"AQID"`) || !strings.Contains(string(response), `"instance_id":"a"`) {
		t.Fatalf("missing compiled response: %s", response)
	}
	response = h.Compile(context.Background(), []byte(input), nats.Header{"Papilio-Deadline": []string{time.Now().Add(-time.Second).Format(time.RFC3339Nano)}})
	if !strings.Contains(string(response), `"error":"deadline_exceeded"`) || compiler.calls != 1 {
		t.Fatal("expired compile reached the native compiler")
	}
	compiler.err = domain.ErrRejected
	response = h.Compile(context.Background(), []byte(input), nil)
	if !strings.Contains(string(response), `"error":"native_rejected"`) || strings.Contains(string(response), `"content"`) {
		t.Fatal("native rejection returned a partial artifact")
	}
}
