package natsapi

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/services"
	"github.com/nats-io/nats.go"
)

func TestResetDeadlineAndMalformedIntent(t *testing.T) {
	reader := &readerFixture{}
	service, _ := services.New([]services.Binding{{ID: "a", Resetter: reader}}, statusFixture{})
	handler := Handler{Service: service, Timeout: time.Second}
	initial, _ := service.ResetStatus(context.Background(), "a", 0, 0)
	input := []byte(fmt.Sprintf(`{"instance_id":"a","epoch":%q,"operation_id":"op","expected_revision":"0","scope":"outbound","name":"A>>>B","direction":"downlink"}`, initial.Epoch))
	if data := handler.ResetCounter(context.Background(), input, nats.Header{"Papilio-Deadline": {"bad"}}); !strings.Contains(string(data), "invalid_argument") || reader.calls != 0 {
		t.Fatal(string(data))
	}
	data := handler.ResetCounter(context.Background(), input, nats.Header{"Papilio-Deadline": {time.Now().Add(200 * time.Millisecond).Format(time.RFC3339Nano)}})
	if !strings.Contains(string(data), `"previous_bytes":"-9007199254740993"`) || reader.calls != 1 || reader.remaining <= 0 || reader.remaining > 200*time.Millisecond {
		t.Fatal(string(data), reader.remaining)
	}
	data = handler.ResetCounter(context.Background(), input, nil)
	if reader.calls != 1 || !strings.Contains(string(data), `"outcome":"completed"`) {
		t.Fatal("replayed", string(data))
	}
}
