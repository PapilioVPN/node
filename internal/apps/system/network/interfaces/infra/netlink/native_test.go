//go:build integration && linux

package netlink

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestActualRouteSocketsCloseAfterRepeatedObservations(t *testing.T) {
	reader := Reader{Timeout: time.Second}
	read := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		rows, err := reader.List(ctx)
		if err != nil || len(rows.Interfaces) == 0 {
			t.Fatalf("native inventory failed: %v", err)
		}
		counters, err := reader.Counters(ctx)
		if err != nil || len(counters) == 0 {
			t.Fatalf("native counters failed: %v", err)
		}
	}
	read() // warm up native/runtime facilities before counting owned process FDs.
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		read()
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("route reads leaked descriptors: before=%d after=%d", len(before), len(after))
	}
}
