package natsapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

func TestStateNATSRejectsMissingOrExtraFields(t *testing.T) {
	h := Handler{Timeout: time.Second}
	for _, data := range []string{`{}`, `{"instance_id":"a","sources":[]}`, `{"instance_id":"a","expected_revision":"0"}`, `{"instance_id":"a","instance_id":"b","expected_revision":"0","sources":[]}`} {
		response := h.Replace(context.Background(), []byte(data), nil)
		if string(response) != `{"error":"invalid_argument"}` {
			t.Fatalf("%s: %s", data, response)
		}
	}
	if string(h.Get(context.Background(), []byte(`{"instance_id":"a","expected_revision":"0"}`), nil)) != `{"error":"invalid_argument"}` {
		t.Fatal("read ignored write selection")
	}
	response := changeResult("a", domain.Change{Persistence: domain.Persistence{PublicationUnknown: true}}, domain.ErrDurabilityUnknown)
	if !strings.Contains(string(response), `"publication_unknown":true`) || !strings.Contains(string(response), `"error":"durability_unknown"`) {
		t.Fatal("lost publication evidence")
	}
}
