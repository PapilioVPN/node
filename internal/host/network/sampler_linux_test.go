package network

import (
	"strings"
	"testing"
	"time"
)

func TestSampleDeltaUsesElapsedTimeAndExcludesLoopback(t *testing.T) {
	const headers = "header\nheader\n"
	before, err := parseCounters(strings.NewReader(headers + " lo: 90 0 0 0 0 0 0 0 90 0 0 0 0 0 0 0\n eth0: 10 0 0 0 0 0 0 0 100 0 0 0 0 0 0 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	after, err := parseCounters(strings.NewReader(headers + " lo: 900 0 0 0 0 0 0 0 900 0 0 0 0 0 0 0\n eth0: 20 0 0 0 0 0 0 0 30 0 0 0 0 0 0 0\n eth1: 50 0 0 0 0 0 0 0 50 0 0 0 0 0 0 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := sampleDelta(before, after, 500*time.Millisecond, time.Now())
	if got == nil || got.RXBytesPerSecond != 20 || got.TXBytesPerSecond != 60 || got.WindowMillis != 500 {
		t.Fatalf("unexpected sample: %+v", got)
	}
	if got := sampleDelta(nil, after, time.Second, time.Now()); got != nil {
		t.Fatalf("missing baseline produced %+v", got)
	}
}
