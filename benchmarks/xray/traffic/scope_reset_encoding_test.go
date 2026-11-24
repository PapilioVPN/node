package traffic

import (
	"fmt"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/native/scopeapi"
	"google.golang.org/protobuf/proto"
)

func BenchmarkScopeResetDecode(b *testing.B) {
	for _, size := range []int{100, 1000} {
		rows := &scopeapi.ScopeResetResult{Counters: make([]*scopeapi.Counter, size)}
		flat := &scopeapi.ScopeCounters{
			Names: make([]string, size), Downlink: make([]bool, size), PreviousBytes: make([]int64, size),
		}
		for i := range size {
			name, downlink := fmt.Sprintf("account-%04d", i/2), i%2 == 1
			rows.Counters[i] = &scopeapi.Counter{Name: name, Downlink: downlink}
			flat.Names[i], flat.Downlink[i] = name, downlink
		}
		for _, tc := range []struct {
			name  string
			reply *scopeapi.ScopeResetResult
		}{{"rows", rows}, {"compact", &scopeapi.ScopeResetResult{CompactCounters: flat}}} {
			b.Run(fmt.Sprintf("%d/%s", size, tc.name), func(b *testing.B) {
				encoded, err := proto.Marshal(tc.reply)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				for b.Loop() {
					var reply scopeapi.ScopeResetResult
					if err := proto.Unmarshal(encoded, &reply); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(len(encoded)), "wire_B")
			})
		}
	}
}
