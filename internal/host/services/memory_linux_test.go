package services

import "testing"

func TestParseMemoryConvertsLinuxKilobytes(t *testing.T) {
	data := []byte("MemTotal: 2048 kB\nMemAvailable: 400 kB\nSwapTotal: 512 kB\nSwapFree: 123 kB\nHugePages_Total: 0\n")
	snapshot, err := parseMemory(data)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.TotalBytes != 2048*1024 || snapshot.SwapTotalBytes != 512*1024 || snapshot.SwapFreeBytes != 123*1024 {
		t.Fatalf("unexpected memory snapshot: %#v", snapshot)
	}
	if snapshot.AvailableBytes == nil || *snapshot.AvailableBytes != 400*1024 {
		t.Fatalf("available RAM = %v, want %d bytes", snapshot.AvailableBytes, 400*1024)
	}
}

func TestParseMemoryPreservesAbsentAndZeroAvailability(t *testing.T) {
	const base = "MemTotal: 1024 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"
	absent, err := parseMemory([]byte(base))
	if err != nil || absent.AvailableBytes != nil || absent.SwapTotalBytes != 0 || absent.SwapFreeBytes != 0 {
		t.Fatalf("snapshot without availability or swap = %#v, %v", absent, err)
	}
	zero, err := parseMemory([]byte(base + "MemAvailable: 0 kB\n"))
	if err != nil || zero.AvailableBytes == nil || *zero.AvailableBytes != 0 {
		t.Fatalf("snapshot with zero availability = %#v, %v", zero, err)
	}
}

func TestParseMemoryRejectsInvalidSamples(t *testing.T) {
	const swap = "SwapTotal: 0 kB\nSwapFree: 0 kB\n"
	for name, input := range map[string]string{
		"missing total": swap,
		"missing swap":  "MemTotal: 1024 kB\nSwapTotal: 0 kB\n",
		"invalid unit":  swap + "MemTotal: 1024 B\n",
		"invalid count": swap + "MemTotal: -1 kB\n",
		"overflow":      swap + "MemTotal: 18014398509481984 kB\n",
		"duplicate":     swap + "MemTotal: 1024 kB\nMemTotal: 2048 kB\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseMemory([]byte(input)); err == nil {
				t.Fatal("invalid sample was accepted")
			}
		})
	}
}

func FuzzParseMemory(f *testing.F) {
	f.Add([]byte("MemTotal: 1 kB\nMemAvailable: 0 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"))
	f.Add([]byte("MemTotal: 18014398509481984 kB\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 16<<10 {
			t.Skip()
		}
		snapshot, err := parseMemory(data)
		if err == nil && (snapshot.TotalBytes%1024 != 0 || snapshot.SwapTotalBytes%1024 != 0 ||
			snapshot.SwapFreeBytes%1024 != 0 || (snapshot.AvailableBytes != nil && *snapshot.AvailableBytes%1024 != 0)) {
			t.Fatalf("sample contains a byte count that did not originate in Linux kB: %#v", snapshot)
		}
	})
}
