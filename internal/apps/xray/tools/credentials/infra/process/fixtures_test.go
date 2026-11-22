package process

import (
	"context"
	"strings"
	"testing"
	"time"
)

func testRunner(t *testing.T) (*Runner, *Executor) {
	t.Helper()
	owner := NewExecutor()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if owner.Close(ctx) != nil {
			t.Error("owner failed to close")
		}
	})
	r, err := New(Config{Binary: "/not-executed", Directory: t.TempDir()}, owner)
	if err != nil {
		t.Fatal(err)
	}
	return r, owner
}
func repeated(n int) []byte { return []byte(strings.Repeat("x", n)) }
