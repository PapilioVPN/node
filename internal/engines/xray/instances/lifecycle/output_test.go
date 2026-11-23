package lifecycle

import (
	"bytes"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/xray/logs/live"
)

func TestOutputFramingResumesAfterAnOversizedLine(t *testing.T) {
	service := live.New("primary")
	run := service.Prepare()
	run.Activate()
	sub, err := service.Subscribe("primary")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	output := outputWriter{run: run, source: live.Stderr}
	prefix := bytes.Repeat([]byte{0xff}, live.MaxMessageBytes)
	for _, part := range [][]byte{prefix[:10], prefix[10:], []byte("discarded\nshort\n\nlast")} {
		if n, err := output.Write(part); err != nil || n != len(part) {
			t.Fatalf("pipe write = %d, %v", n, err)
		}
	}
	output.finish()
	for i, want := range []struct {
		message   string
		truncated bool
	}{{string(prefix), true}, {"short", false}, {"", false}, {"last", false}} {
		event, err := sub.Next(t.Context())
		if err != nil || event.Source != live.Stderr || event.Sequence != uint64(i+1) ||
			event.Message != want.message || event.Truncated != want.truncated {
			t.Fatalf("event %d has %d bytes (truncated %t): %v", i+1, len(event.Message), event.Truncated, err)
		}
	}
}
