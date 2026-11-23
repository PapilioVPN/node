package runtime

import "errors"

var (
	ErrUnavailable = errors.New("xray runtime statistics unavailable")
	ErrBusy        = errors.New("too many concurrent xray statistics reads")
)

type Snapshot struct {
	Goroutines              uint32
	GCCount                 uint32
	HeapAllocatedBytes      uint64
	TotalAllocatedBytes     uint64
	SystemBytes             uint64
	Mallocs                 uint64
	Frees                   uint64
	LiveObjects             uint64
	GCPauseTotalNanoseconds uint64
	UptimeSeconds           uint32
}
