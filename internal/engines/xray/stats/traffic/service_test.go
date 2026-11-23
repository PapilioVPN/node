package traffic

import (
	"context"
	"errors"
	"testing"
)

type record struct {
	name  string
	bytes int64
}

func (service records) Query(ctx context.Context, id, pattern string, maxBytes int) (Counters, error) {
	service.calls <- pattern
	return Counters{Count: len(service.entries), Each: func(yield func(string, int64) bool) {
		for _, entry := range service.entries {
			if !yield(entry.name, entry.bytes) {
				break
			}
		}
	}}, nil
}

type records struct {
	entries []record
	calls   chan string
}

func TestExactAndScopedCounterPresence(t *testing.T) {
	fixture := records{entries: []record{
		{"user>>>team>>>alice>>>traffic>>>uplink", 0},
		{"user>>>alice>>>traffic>>>downlink", 23},
		{"inbound>>>shared>>>user>>>team>>>alice>>>traffic>>>downlink", 123},
		{"user>>>other>>>online>>>count", 400},
		{"user>>>>>>traffic>>>uplink", 400},
	}, calls: make(chan string, 3)}
	service := New(fixture)
	entry, err := service.Get(t.Context(), "xray", User, "team>>>alice")
	if err != nil || entry != (Usage{Name: "team>>>alice", HasUplink: true}) {
		t.Fatalf("zero uplink and absent downlink: %+v, %v", entry, err)
	}
	if call := <-fixture.calls; call != "user>>>team>>>alice>>>traffic>>>" {
		t.Fatalf("exact query = %q", call)
	}
	entries, err := service.List(t.Context(), "xray", User)
	if err != nil || len(entries) != 2 || entries[0] != entry ||
		entries[1] != (Usage{Name: "alice", DownlinkBytes: 23, HasDownlink: true}) {
		t.Fatalf("scoped counters = %+v, %v", entries, err)
	}
	if call := <-fixture.calls; call != "user>>>" {
		t.Fatalf("list query = %q", call)
	}
	_, err = service.Get(t.Context(), "xray", User, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent pair = %v", err)
	}
}
