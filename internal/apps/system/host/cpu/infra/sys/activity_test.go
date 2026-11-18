//go:build linux

package sys

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

func TestActivityRetainsExactCountersAndCurrentGauges(t *testing.T) {
	got, err := readActivity(context.Background(), procStat(t, activityFixture))
	want := domain.Activity{ContextSwitches: 9007199254740993, Interrupts: 1<<64 - 1, SoftInterrupts: 200, CreatedTasks: 300, RunnableTasks: 0, IOBlockedTasks: 2}
	if err != nil || got != want {
		t.Fatalf("activity: %+v %v", got, err)
	}
	// A large interrupt vector and thousands of unselected CPU rows remain one
	// stream; only six totals/gauges are materialized, not a per-item inventory.
	large := strings.Repeat("cpu1 1 2 3 4 5 6 7 8 9 10\n", 4096) + strings.Replace(activityFixture, " 1 2 3\n", " "+strings.Repeat("0 ", 300000)+"\n", 1)
	got, err = parseActivity(context.Background(), strings.NewReader(large))
	if err != nil || got != want {
		t.Fatalf("large activity: %+v %v", got, err)
	}
}

func TestActivityRejectsIncompleteMalformedOrUnboundedSources(t *testing.T) {
	for _, input := range []string{"", strings.Replace(activityFixture, "ctxt 9007199254740993\n", "", 1), activityFixture + "ctxt 1\n", strings.Replace(activityFixture, "ctxt 9007199254740993", "ctxt 1 2", 1), strings.Replace(activityFixture, "ctxt 9007199254740993", "ctxt -1", 1), strings.Replace(activityFixture, "ctxt 9007199254740993", "ctxt 18446744073709551616", 1), strings.Repeat("x", 1<<20) + "\n" + activityFixture} {
		got, err := parseActivity(context.Background(), strings.NewReader(input))
		if err == nil || got != (domain.Activity{}) {
			t.Fatalf("bad source accepted: %+v %v", got, err)
		}
	}
	_, err := parseActivity(context.Background(), strings.NewReader(strings.Replace(activityFixture, "ctxt 9007199254740993", "ctxt bad", 1)))
	var number *strconv.NumError
	if !errors.As(err, &number) {
		t.Fatalf("lost numeric cause: %v", err)
	}
	cause := io.ErrUnexpectedEOF
	got, err := parseActivity(context.Background(), readerFunc(func([]byte) (int, error) { return 0, cause }))
	if !errors.Is(err, cause) || got != (domain.Activity{}) {
		t.Fatalf("read cause: %+v %v", got, err)
	}
	got, err = parseActivity(context.Background(), &repeatingLine{line: strings.Repeat("x", 1023) + "\n"})
	if err == nil || got != (domain.Activity{}) {
		t.Fatalf("byte limit ignored: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err = readActivity(ctx, t.TempDir())
	if !errors.Is(err, context.Canceled) || got != (domain.Activity{}) {
		t.Fatalf("canceled activity: %+v %v", got, err)
	}
}

func FuzzActivity(f *testing.F) {
	f.Add(activityFixture)
	f.Add("intr 1 2 3\n")
	f.Add("")
	f.Fuzz(func(t *testing.T, input string) {
		got, err := parseActivity(context.Background(), strings.NewReader(input))
		if err != nil && got != (domain.Activity{}) {
			t.Fatalf("partial facts on failure: %+v %v", got, err)
		}
	})
}
