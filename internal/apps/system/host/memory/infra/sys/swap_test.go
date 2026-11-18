package sys

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
)

func TestCapacityUsesKernelUnitsAndAllowsDisabledSwapWithHistoricalIO(t *testing.T) {
	for _, test := range []struct {
		total, free, unit, in, out uint64
		want                       domain.SwapSnapshot
	}{
		{0, 0, 1, 8192, 12288, domain.SwapSnapshot{SwappedInBytes: 8192, SwappedOutBytes: 12288}},
		{8, 2, 4096, 1, 2, domain.SwapSnapshot{TotalBytes: 32768, FreeBytes: 8192, UsedBytes: 24576, UsedPercent: 75, SwappedInBytes: 1, SwappedOutBytes: 2}},
	} {
		got, err := capacity(test.total, test.free, test.unit, test.in, test.out)
		if err != nil || got != test.want {
			t.Fatalf("capacity: %+v %v; want %+v", got, err, test.want)
		}
	}
	for _, test := range [][3]uint64{{1, 2, 1}, {1, 0, 0}, {math.MaxUint64, 0, 2}} {
		got, err := capacity(test[0], test[1], test[2], 0, 0)
		if err == nil || got != (domain.SwapSnapshot{}) {
			t.Fatalf("invalid capacity accepted: %+v %v", got, err)
		}
	}
}
func TestIOUsesActualPageSizeAndPreservesFullCounterPrecision(t *testing.T) {
	for _, page := range []uint64{4096, 8192, 65536} {
		in, out, err := readIO(context.Background(), strings.NewReader("unknown 42\npswpout 3\npswpin 2\n"), page)
		if err != nil || in != 2*page || out != 3*page {
			t.Fatalf("page size %d: %d %d %v", page, in, out, err)
		}
	}
	in, out, err := readIO(context.Background(), strings.NewReader(fmt.Sprintf("pswpin %d\npswpout 0\n", uint64(math.MaxUint64))), 1)
	if err != nil || in != math.MaxUint64 || out != 0 {
		t.Fatalf("precision: %d %d %v", in, out, err)
	}
}
func TestIORejectsAbsenceDuplicatesMalformedValuesAndOverflow(t *testing.T) {
	for _, input := range []string{"", "pswpin 1\n", "pswpin 0\npswpin 1\npswpout 0\n", "pswpout 0\npswpout 1\npswpin 0\n", "pswpin -1\npswpout 0\n", "pswpin 1 extra\npswpout 0\n", fmt.Sprintf("pswpin %d\npswpout 0\n", uint64(math.MaxUint64))} {
		in, out, err := readIO(context.Background(), strings.NewReader(input), 4096)
		if err == nil || in != 0 || out != 0 {
			t.Fatalf("invalid source returned counters: %d %d %v", in, out, err)
		}
	}
	_, _, err := readIO(context.Background(), strings.NewReader("pswpin invalid\npswpout 0\n"), 4096)
	var parse *strconv.NumError
	if !errors.As(err, &parse) {
		t.Fatalf("lost native parse cause: %v", err)
	}
}
func TestIOBoundsSourceAndRetainsReadErrors(t *testing.T) {
	source := strings.NewReader(strings.Repeat("unknown 0\n", vmstatLimit/2))
	_, _, err := readIO(context.Background(), source, 4096)
	if err == nil || source.Size()-int64(source.Len()) > vmstatLimit+1 {
		t.Fatal("source read is not bounded")
	}
	_, _, err = readIO(context.Background(), strings.NewReader(strings.Repeat("x", 128<<10)), 4096)
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("lost native scanner bound: %v", err)
	}
	cause := errors.New("controlled read failure")
	_, _, err = readIO(context.Background(), readerFunc(func([]byte) (int, error) { return 0, cause }), 4096)
	if !errors.Is(err, cause) {
		t.Fatalf("lost source cause: %v", err)
	}
}
func TestIOCancellationBeforeAndDuringReadReturnsNoCounters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := readIO(ctx, readerFunc(func([]byte) (int, error) { panic("canceled read performed I/O") }), 4096); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	in, out, err := readIO(ctx, readerFunc(func(p []byte) (int, error) { cancel(); return copy(p, "pswpin 1\npswpout 2\n"), nil }), 4096)
	if !errors.Is(err, context.Canceled) || in != 0 || out != 0 {
		t.Fatalf("partial canceled counters: %d %d %v", in, out, err)
	}
}
