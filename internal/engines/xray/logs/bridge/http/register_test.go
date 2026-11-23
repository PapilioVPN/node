package http

import (
	"encoding/json"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/xray/logs/live"
)

func TestAppendRecordPreservesWireBytes(t *testing.T) {
	buffer := make([]byte, 0, 256)
	for _, event := range []live.Event{
		{Execution: 23, Sequence: 42, Source: live.Stderr, ReceivedUnixNano: -12, Message: "\xff\n\x00", Truncated: true},
		{Execution: 7, Sequence: 9, Source: live.Stdout, ReceivedUnixNano: 13},
	} {
		want, err := json.Marshal(struct {
			Execution        uint64      `json:"execution,string"`
			Sequence         uint64      `json:"sequence,string"`
			Source           live.Source `json:"source"`
			ReceivedUnixNano int64       `json:"received_unix_nano,string"`
			Message          []byte      `json:"message"`
			Truncated        bool        `json:"truncated"`
		}{event.Execution, event.Sequence, event.Source, event.ReceivedUnixNano, []byte(event.Message), event.Truncated})
		if err != nil {
			t.Fatal(err)
		}
		buffer = appendRecord(buffer[:0], event)
		if got := string(buffer); got != string(want)+"\n" {
			t.Fatalf("encoded log %q, want %s", got, want)
		}
	}
}
