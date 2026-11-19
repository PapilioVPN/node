//go:build integration && linux

package sys

import (
	"context"
	"os"
	"testing"
)

func TestNativeSensorsReleaseDescriptors(t *testing.T) {
	ctx := context.Background()
	r := Reader{}
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err := r.Temperatures(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Fans(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := r.PowerSupplies(ctx); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) > len(before) {
		t.Fatalf("descriptors %d -> %d", len(before), len(after))
	}
}
