package sys

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestInfoStreamsSparseIdentitiesAndPreservesUnitsAndAbsence(t *testing.T) {
	input := "processor : 4\nvendor_id : Vendor\ncpu family : 6\nmodel : 23\nmodel name : Model: with colon\nstepping : 0\nmicrocode : 0x123\nphysical id : 0\ncore id : 0\ncpu cores : 2\nsiblings : 4\ncpu MHz : 1234.5\ncache size : 512 KB\nflags : first second first\nbugs : issue\n\nprocessor : 9\n"
	rows, err := readInfo(context.Background(), cpuInfoFixture(t, input))
	if err != nil || len(rows) != 2 {
		t.Fatalf("inventory: %+v %v", rows, err)
	}
	got := rows[0]
	if got.ID != 4 || got.VendorID == nil || *got.VendorID != "Vendor" || got.Family == nil || *got.Family != "6" || got.Model == nil || *got.Model != "23" || got.ModelName == nil || *got.ModelName != "Model: with colon" || got.Stepping == nil || *got.Stepping != "0" || got.Microcode == nil || *got.Microcode != "0x123" || got.PackageID == nil || *got.PackageID != 0 || got.CoreID == nil || *got.CoreID != 0 || got.ReportedCores == nil || *got.ReportedCores != 2 || got.ReportedSiblings == nil || *got.ReportedSiblings != 4 || got.ReportedMHz == nil || *got.ReportedMHz != 1234.5 || got.CacheBytes == nil || *got.CacheBytes != 512*1024 || strings.Join(got.ReportedFeatures, " ") != "first second first" || strings.Join(got.ReportedBugs, " ") != "issue" {
		t.Fatalf("lost metadata/presence/units: %+v", got)
	}
	missing := rows[1]
	if missing.ID != 9 || missing.VendorID != nil || missing.ModelName != nil || missing.PackageID != nil || missing.CoreID != nil || missing.ReportedMHz != nil || missing.CacheBytes != nil || len(missing.ReportedFeatures) != 0 {
		t.Fatalf("inherited or fabricated metadata: %+v", missing)
	}
	rows, err = parseInfo(context.Background(), strings.NewReader("Processor : ARM model\nprocessor : 0\nCPU implementer : 0x41\nCPU part : 0xd03\nCPU revision : 0\nFeatures : fp asimd\nphysical id : -1\n"))
	if err != nil || len(rows) != 1 || rows[0].ModelName == nil || *rows[0].ModelName != "ARM model" || rows[0].PackageID != nil || rows[0].VendorID == nil || *rows[0].VendorID != "0x41" {
		t.Fatalf("architecture metadata: %+v %v", rows, err)
	}
}

func TestInfoDoesNotTruncateInventoriesBeyondOneMiB(t *testing.T) {
	var source strings.Builder
	for i := 0; i < 4096; i++ {
		fmt.Fprintf(&source, "processor : %d\nphysical id : %d\ncpu cores : 8\nmodel name : %s\n\n", i, i/16, strings.Repeat("model", 64))
	}
	if source.Len() <= 1<<20 {
		t.Fatal("fixture did not reach the native SDK truncation boundary")
	}
	rows, err := parseInfo(context.Background(), strings.NewReader(source.String()))
	if err != nil || len(rows) != 4096 || rows[4095].ID != 4095 || rows[4095].ModelName == nil || len(*rows[4095].ModelName) != 320 {
		t.Fatalf("truncated inventory: %d rows %v", len(rows), err)
	}
}

func TestInfoRejectsDuplicatesMalformedFieldsAndPartialIO(t *testing.T) {
	for _, source := range []string{"", "model name: no processor\n", "processor: -1\n", "processor: 1\nprocessor: 1\n", "processor: 1\ncache size: 1 MB\n", "processor: 1\ncache size: 18446744073709551615 KB\n", "processor: 1\ncpu MHz: NaN\n", "processor: 1\ncpu MHz: +Inf\n", "processor: 1\ncpu MHz: -1\n", "processor: 1\nphysical id: -2\n", "processor: 1\ncpu cores: 4294967296\n"} {
		if rows, err := parseInfo(context.Background(), strings.NewReader(source)); err == nil || rows != nil {
			t.Fatalf("invalid inventory returned rows: %d %v", len(rows), err)
		}
	}
	_, err := parseInfo(context.Background(), strings.NewReader("processor: 1\ncache size: invalid KB\n"))
	var number *strconv.NumError
	if !errors.As(err, &number) {
		t.Fatalf("lost parse cause: %v", err)
	}
	cause := errors.New("controlled native read failure")
	_, err = parseInfo(context.Background(), io.MultiReader(strings.NewReader("processor: 1\n"), readerFunc(func([]byte) (int, error) { return 0, cause })))
	if !errors.Is(err, cause) {
		t.Fatalf("lost native source cause: %v", err)
	}
	_, err = readInfo(context.Background(), t.TempDir())
	var path *os.PathError
	if !errors.As(err, &path) {
		t.Fatalf("lost file cause: %v", err)
	}
}

func TestInfoBoundsSourceLinesRecordsAndCancellation(t *testing.T) {
	_, err := parseInfo(context.Background(), strings.NewReader("processor: 0\nflags: "+strings.Repeat("x", 128<<10)))
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("lost scanner bound: %v", err)
	}
	var source strings.Builder
	for i := 0; i <= cpuInfoProcessors; i++ {
		fmt.Fprintf(&source, "processor: %d\n", i)
	}
	if rows, err := parseInfo(context.Background(), strings.NewReader(source.String())); err == nil || rows != nil {
		t.Fatal("processor capacity not enforced")
	}
	// Long but bounded unknown lines exercise the byte cap with only 1025 records.
	line := strings.Repeat("x", (64<<10)-2) + "\n"
	bounded := io.LimitReader(&repeatingLine{line: line}, cpuInfoLimit+2)
	if rows, err := parseInfo(context.Background(), bounded); err == nil || rows != nil {
		t.Fatal("byte capacity not enforced")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rows, err := parseInfo(ctx, readerFunc(func([]byte) (int, error) { panic("pre-canceled inventory read performed IO") })); !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatalf("canceled inventory: %+v %v", rows, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	rows, err := parseInfo(ctx, readerFunc(func(p []byte) (int, error) { cancel(); return copy(p, "processor: 0\n"), nil }))
	if !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatalf("partial canceled inventory: %+v %v", rows, err)
	}
}

func FuzzCPUInfo(f *testing.F) {
	f.Add("processor : 0\nphysical id : 0\ncpu cores : 4\nflags : a b\n")
	f.Add("processor: 4294967296\n")
	f.Add("processor: 0\ncpu MHz: NaN\n")
	f.Fuzz(func(t *testing.T, source string) {
		rows, err := parseInfo(context.Background(), strings.NewReader(source))
		if err != nil {
			if rows != nil {
				t.Fatal("partial inventory on error")
			}
			return
		}
		if len(rows) == 0 || len(rows) > cpuInfoProcessors {
			t.Fatal("invalid inventory size")
		}
		seen := map[uint32]bool{}
		for _, row := range rows {
			if seen[row.ID] {
				t.Fatal("duplicate identity")
			}
			seen[row.ID] = true
			if row.ReportedMHz != nil && (math.IsNaN(*row.ReportedMHz) || math.IsInf(*row.ReportedMHz, 0) || *row.ReportedMHz < 0) {
				t.Fatal("invalid frequency")
			}
		}
	})
}
