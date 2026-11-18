package natsapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/natsapi"
	"github.com/nats-io/nats.go"
)

func TestNATSInterfacesPreserveAddressesPrecisionAndMissingCounters(t *testing.T) {
	reader := &readerFixture{}
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader}}
	list := handler.List(context.Background(), []byte("{}"), nil)
	var inventory struct {
		Interfaces []struct {
			Index     uint32
			Addresses []struct {
				Peer  *string `json:"peer_prefix"`
				Valid uint32  `json:"valid_lifetime_seconds"`
			}
		}
		Unmatched []struct {
			Index uint32 `json:"interface_index"`
		} `json:"unmatched_addresses"`
	}
	if err := json.Unmarshal(list, &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory.Interfaces) != 1 || inventory.Interfaces[0].Index != 42 || len(inventory.Interfaces[0].Addresses) != 1 || inventory.Interfaces[0].Addresses[0].Peer == nil || *inventory.Interfaces[0].Addresses[0].Peer != "198.51.100.5/24" || inventory.Interfaces[0].Addresses[0].Valid != 1<<32-1 || len(inventory.Unmatched) != 1 || inventory.Unmatched[0].Index != 77 {
		t.Fatalf("lost addresses: %s", list)
	}
	body := handler.ReadCounters(context.Background(), []byte("{}"), nil)
	var counters struct {
		Interfaces []struct{ Counters *map[string]string }
	}
	if err := json.Unmarshal(body, &counters); err != nil {
		t.Fatal(err)
	}
	if len(counters.Interfaces) != 2 || counters.Interfaces[0].Counters == nil || (*counters.Interfaces[0].Counters)["receive_bytes"] != "9007199254740993" || (*counters.Interfaces[0].Counters)["transmit_bytes"] != "18446744073709551615" || counters.Interfaces[1].Counters != nil || reader.calls != 1 || reader.counterCalls != 1 {
		t.Fatalf("lost counters: %s", body)
	}
}

func TestNATSInterfaceInputAndDeadlineBounds(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second}
	for _, call := range []func(context.Context, []byte, nats.Header) []byte{handler.List, handler.ReadCounters} {
		for _, input := range []string{"", "null", "[]", "{}{}", `{"x":1}`, strings.Repeat(" ", 1025)} {
			got := call(context.Background(), []byte(input), nil)
			if string(got) != `{"error":"invalid_argument"}` && string(got) != `{"error":"resource_exhausted"}` {
				t.Fatal(string(got))
			}
		}
		for _, values := range [][]string{{"bad"}, {"bad", "duplicate"}} {
			if got := call(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": values}); string(got) != `{"error":"invalid_argument"}` {
				t.Fatal(string(got))
			}
		}
	}
	handler.Timeout = 10 * time.Millisecond
	handler.Service.Reader = &readerFixture{wait: true}
	for _, call := range []func(context.Context, []byte, nats.Header) []byte{handler.List, handler.ReadCounters} {
		for _, deadline := range []time.Time{time.Now().Add(-time.Second), time.Now().Add(time.Hour)} {
			got := call(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": {deadline.UTC().Format(time.RFC3339Nano)}})
			if string(got) != `{"error":"deadline_exceeded"}` {
				t.Fatal(string(got))
			}
		}
	}
	handler.Service.Reader = &readerFixture{cause: errors.New("private socket")}
	for _, call := range []func(context.Context, []byte, nats.Header) []byte{handler.List, handler.ReadCounters} {
		if got := call(context.Background(), []byte("{}"), nil); string(got) != `{"error":"unavailable"}` {
			t.Fatal(string(got))
		}
	}
}
