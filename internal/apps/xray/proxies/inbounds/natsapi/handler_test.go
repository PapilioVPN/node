package natsapi_test

import (
	"context"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/natsapi"
	"github.com/nats-io/nats.go"
)

func TestInvalidShapeAndDeadlineNeverReachService(t *testing.T) {
	h := natsapi.Handler{}
	for _, test := range []struct {
		call    func(context.Context, []byte, nats.Header) []byte
		body    string
		headers nats.Header
	}{
		{h.AddSources, `{"instance_id":"a","tag":"b","sources":[],"configuration":"AQ=="}`, nil},
		{h.AddSources, `{"instance_id":"a","sources":[]}`, nil},
		{h.Summaries, `{"instance_id":"a","tag":"b"}`, nil},
		{h.Compatibility, `{"instance_id":"a"}`, nil},
		{h.Plan, `{"instance_id":"a","tag":"b","configuration":"AQ==","sources":[]}`, nil},
		{h.Plan, `{"instance_id":"a","tag":"b","configuration":"AQ==","disconnect_existing":null}`, nil},
		{h.Replace, `{"instance_id":"a","token":"x","allow_interruption":true}`, nil},
		{h.Add, `{"instance_id":"a","configuration":"AQ==","page_size":0}`, nil},
		{h.Records, `{"instance_id":"a","tag":"b"}`, nil}, {h.Tags, `{"instance_id":"a","configuration":"AQ=="}`, nil},
		{h.Add, `{"instance_id":"a"}`, nil}, {h.Remove, `{"instance_id":"a","configuration":"AQ=="}`, nil},
		{h.Status, `{"instance_id":"a","instance_id":"b"}`, nil}, {h.Status, `{"instance_id":null}`, nil},
		{h.Tags, `{"instance_id":"a"}`, nats.Header{"Papilio-Deadline": {"invalid"}}},
	} {
		if response := test.call(context.Background(), []byte(test.body), test.headers); !strings.Contains(string(response), `"error":"invalid_argument"`) {
			t.Fatalf("%s", response)
		}
	}
}
