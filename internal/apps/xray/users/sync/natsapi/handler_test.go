package natsapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/ports"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/natsapi"
	"github.com/nats-io/nats.go"
)

type controlled struct {
	ports.Operations
	calls    int
	deadline time.Time
}

func (c *controlled) Apply(ctx context.Context, _ string, _ string, r domain.Revision) (domain.ReceiptPage, error) {
	c.calls++
	c.deadline, _ = ctx.Deadline()
	return domain.ReceiptPage{Receipt: domain.Receipt{InstanceID: "one", OperationID: "op", Revision: r, Effect: accounts.Unknown}}, errors.New("private credential diagnostic")
}
func TestStrictDeadlineAndSafeOperationalEnvelope(t *testing.T) {
	c := &controlled{}
	h := natsapi.Handler{Service: c, Timeout: time.Minute}
	input := []byte(`{"instance_id":"one","plan_id":"p","expected":{"epoch":"epoch"}}`)
	for _, headers := range []nats.Header{{"Papilio-Deadline": {"invalid"}}, {"Papilio-Deadline": {"one", "two"}}} {
		data := h.Apply(context.Background(), input, headers)
		if !strings.Contains(string(data), "invalid_argument") || c.calls != 0 {
			t.Fatal(string(data), c.calls)
		}
	}
	deadline := time.Now().Add(time.Second)
	data := h.Apply(context.Background(), input, nats.Header{"Papilio-Deadline": {deadline.Format(time.RFC3339Nano)}})
	if c.calls != 1 || !c.deadline.Equal(deadline) || !strings.Contains(string(data), `"effect":"unknown"`) || !strings.Contains(string(data), `"error":"unavailable"`) || strings.Contains(string(data), "private") {
		t.Fatal(string(data), c.deadline)
	}
}
