package sys

import (
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/host/load/domain"
)

func TestKernelFixedPointLoadConversion(t *testing.T) {
	for _, test := range []struct {
		one, five, fifteen uint64
		want               domain.Averages
	}{
		{0, 0, 0, domain.Averages{}},
		{1 << 16, 5 << 16, 15 << 16, domain.Averages{OneMinute: 1, FiveMinutes: 5, FifteenMinutes: 15}},
		{1, (3 << 15), (1024 << 16) + 32768, domain.Averages{OneMinute: 1.0 / 65536, FiveMinutes: 1.5, FifteenMinutes: 1024.5}},
	} {
		if got := averages(test.one, test.five, test.fifteen); got != test.want {
			t.Fatalf("conversion: %+v; want %+v", got, test.want)
		}
	}
}
