package natsapi_test

import (
	"context"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/natsapi"
	"github.com/nats-io/nats.go"
)

func TestMalformedSelectionNeverReachesService(t *testing.T) {
	h := natsapi.Handler{}
	for _, body := range []string{`{}`, `{"instance_id":"a"}`, `{"instance_id":null,"inbound_tag":"b"}`, `{"instance_id":"a","inbound_tag":"b","inbound_tag":"c"}`, `{"instance_id":"a","inbound_tag":"b"} {}`, `{"instance_id":"a","inbound_tag":"b","unknown":"c"}`} {
		for _, call := range []func(context.Context, []byte, nats.Header) []byte{h.Count, h.Get} {
			if result := call(context.Background(), []byte(body), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
				t.Fatal(body, string(result))
			}
		}
	}
	if result := h.Count(context.Background(), []byte(`{"instance_id":"a","inbound_tag":"b","email":"c"}`), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
		t.Fatal(string(result))
	}
	if result := h.Get(context.Background(), []byte(`{"instance_id":"a","inbound_tag":"b"}`), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
		t.Fatal(string(result))
	}
	for _, headers := range []nats.Header{{"Papilio-Deadline": {"bad"}}, {"Papilio-Deadline": {"a", "b"}}} {
		if result := h.Count(context.Background(), []byte(`{"instance_id":"a","inbound_tag":"b"}`), headers); !strings.Contains(string(result), `"error":"invalid_argument"`) {
			t.Fatal(string(result))
		}
	}
}

func TestInvalidBatchEnvelopesNeverReachService(t *testing.T) {
	h := natsapi.Handler{}
	for _, call := range []func(context.Context, []byte, nats.Header) []byte{h.AddMany, h.RemoveMany} {
		for _, body := range []string{`{}`, `{"instance_id":"a","items":null}`, `{"items":[{"inbound_tag":"a","email":"x"}]}`, `{"instance_id":"a","items":[{"inbound_tag":"a","email":null}]}`} {
			if result := call(context.Background(), []byte(body), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
				t.Fatal(body, string(result))
			}
		}
	}
}

func TestInvalidConfigurationPreparationNeverReachesService(t *testing.T) {
	h := natsapi.Handler{}
	for _, body := range []string{`{}`, `{"sources":[],"inbounds":null}`, `{"sources":[],"inbounds":[]}`} {
		if result := h.PrepareConfiguration(context.Background(), []byte(body), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
			t.Fatal(string(result))
		}
	}
}

func TestMalformedControlRequestsNeverReachService(t *testing.T) {
	h := natsapi.Handler{}
	for _, test := range []struct {
		call func(context.Context, []byte, nats.Header) []byte
		body string
	}{
		{h.Add, `{"instance_id":"a","inbound_tag":"b","email":"c","configuration":"AQ=="}`},
		{h.Add, `{"instance_id":"a","inbound_tag":"b"}`},
		{h.Add, `{"instance_id":"a","inbound_tag":"b","configuration":"AQ==","configuration":"Ag=="}`},
		{h.Remove, `{"instance_id":"a","inbound_tag":"b"}`},
		{h.Remove, `{"instance_id":"a","inbound_tag":"b","email":null}`},
		{h.Remove, `{"instance_id":"a","inbound_tag":"b","email":"c","configuration":"AQ=="}`},
		{h.Status, `{"instance_id":"a","email":"c"}`},
		{h.Status, `{"instance_id":"a"} {}`},
	} {
		if result := test.call(context.Background(), []byte(test.body), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
			t.Fatal(test.body, string(result))
		}
	}
}

func TestSourceEnvelopesDoNotChangeRawControlSemantics(t *testing.T) {
	h := natsapi.Handler{}
	for _, body := range []string{
		`{"instance_id":"a","inbound_tag":"b","configuration":"AQ==","sources":[]}`,
		`{"instance_id":"a","inbound_tag":"b","email":"a","sources":null}`,
		`{"instance_id":"a","inbound_tag":"b","sources":[]}`,
	} {
		if result := h.AddSources(context.Background(), []byte(body), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
			t.Fatal(string(result))
		}
	}
	if result := h.Add(context.Background(), []byte(`{"instance_id":"a","inbound_tag":"b","configuration":"AQ==","sources":[]}`), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
		t.Fatal(string(result))
	}
	if result := h.Remove(context.Background(), []byte(`{"instance_id":"a","inbound_tag":"b","email":"a","sources":[]}`), nil); !strings.Contains(string(result), `"error":"invalid_argument"`) {
		t.Fatal(string(result))
	}
}
