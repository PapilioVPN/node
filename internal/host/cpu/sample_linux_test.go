package cpu

import (
	"testing"
	"time"
)

func TestCPUDeltaCountsBusyAndStealSeparately(t *testing.T) {
	start := time.Unix(1, 0)
	end := start.Add(1250 * time.Millisecond)
	previous := cpuReadingForTest(t, "cpu 100 20 80 600 40 3 10 10 6 1", start)
	current := cpuReadingForTest(t, "cpu 125 25 85 640 50 5 13 20 25 5", end)

	sample := cpuDelta(previous, current)
	if sample == nil || sample.BusyPercent != 40 || !sample.HasSteal || sample.StealPercent != 10 ||
		sample.Window != 1250*time.Millisecond || !sample.ObservedAt.Equal(end) {
		t.Fatalf("CPU sample = %+v, want 40%% busy, 10%% steal and a 1250ms window ending at %s", sample, end)
	}
}

func TestCPUDeltaPreservesStealPresence(t *testing.T) {
	start := time.Unix(1, 0)
	for _, test := range []struct {
		name string
		line string
		want bool
	}{
		{name: "missing", line: " 0 0 0"},
		{name: "present with zero change", line: " 0 0 0 5", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := cpuReadingForTest(t, "cpu 100 0 0 100"+test.line, start)
			current := cpuReadingForTest(t, "cpu 100 0 0 140"+test.line, start.Add(time.Second))
			sample := cpuDelta(previous, current)
			if sample == nil || sample.HasSteal != test.want || sample.BusyPercent != 0 || sample.StealPercent != 0 {
				t.Fatalf("CPU sample = %+v, want zero busy, zero steal and steal presence %t", sample, test.want)
			}
		})
	}
}

func TestCPUDeltaRejectsInvalidIntervals(t *testing.T) {
	start := time.Unix(1, 0)
	const first = "cpu 100 0 0 100 10 0 0 0"
	previous := cpuReadingForTest(t, first, start)
	for _, test := range []struct {
		name string
		line string
		end  time.Time
	}{
		{name: "counter rollback", line: "cpu 102 0 0 120 9 0 0 0", end: start.Add(time.Second)},
		{name: "changed counter layout", line: "cpu 101 0 0 120", end: start.Add(time.Second)},
		{name: "frozen counters", line: first, end: start.Add(time.Second)},
		{name: "zero window", line: "cpu 101 0 0 120 10 0 0 0", end: start},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := cpuReadingForTest(t, test.line, test.end)
			if sample := cpuDelta(previous, current); sample != nil {
				t.Fatalf("invalid interval returned %+v", sample)
			}
		})
	}
}

func TestParseCPURejectsInvalidAggregate(t *testing.T) {
	for _, line := range []string{"", "cpu0 1 2 3 4", "cpu 1 2 3", "cpu 1 x 3 4", "cpu 18446744073709551616 2 3 4"} {
		if _, err := parseCPU([]byte(line), time.Time{}); err == nil {
			t.Errorf("accepted invalid aggregate %q", line)
		}
	}
}

func FuzzParseCPU(f *testing.F) {
	f.Add([]byte("cpu 1 2 3 4 5 6 7 8 9 10\n"))
	f.Add([]byte("cpu0 1 2 3 4\n"))
	f.Fuzz(func(t *testing.T, line []byte) {
		if len(line) > 512 {
			return
		}
		reading, err := parseCPU(line, time.Time{})
		if err == nil && (reading.count < 4 || reading.count > cpuCounterCount) {
			t.Fatalf("invalid aggregate counter count %d", reading.count)
		}
	})
}

func cpuReadingForTest(t *testing.T, line string, at time.Time) cpuReading {
	t.Helper()
	reading, err := parseCPU([]byte(line), at)
	if err != nil {
		t.Fatal(err)
	}
	return reading
}
