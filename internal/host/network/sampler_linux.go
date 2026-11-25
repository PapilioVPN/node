// Package network observes byte rates in Node's Linux network namespace.
package network

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"math/bits"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

type Sample struct {
	RXBytesPerSecond uint64
	TXBytesPerSecond uint64
	WindowMillis     uint64
	ObservedAt       time.Time
}

type Sampler struct {
	latest atomic.Pointer[Sample]
	cancel context.CancelFunc
	done   chan struct{}
}

func NewSampler() *Sampler { return &Sampler{} }

func (s *Sampler) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	run, cancel := context.WithCancel(context.Background())
	s.cancel, s.done = cancel, make(chan struct{})
	go s.run(run)
	return nil
}

func (s *Sampler) Stop(ctx context.Context) error {
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Sampler) Latest() (Sample, bool) {
	if value := s.latest.Load(); value != nil {
		return *value, true
	}
	return Sample{}, false
}

type counters struct{ rx, tx uint64 }

func (s *Sampler) run(ctx context.Context) {
	defer close(s.done)
	defer s.latest.Store(nil)
	before, at := readCounters()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			after, now := readCounters()
			s.latest.Store(sampleDelta(before, after, now.Sub(at), now.UTC()))
			before, at = after, now
		}
	}
}

func readCounters() (map[string]counters, time.Time) {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil, time.Time{}
	}
	defer file.Close()
	snapshot, err := parseCounters(file)
	if err != nil {
		return nil, time.Time{}
	}
	return snapshot, time.Now()
}

func parseCounters(reader io.Reader) (map[string]counters, error) {
	scan := bufio.NewScanner(reader)
	for range 2 {
		if !scan.Scan() {
			return nil, errors.New("missing Linux interface counters header")
		}
	}
	devices := make(map[string]counters)
	for scan.Scan() {
		name, values, found := bytes.Cut(scan.Bytes(), []byte(":"))
		name = bytes.TrimSpace(name)
		if !found || len(name) == 0 || len(devices) >= 4096 {
			return nil, errors.New("invalid Linux interface counters")
		}
		if bytes.Equal(name, []byte("lo")) {
			continue
		}
		fields := bytes.Fields(values)
		if len(fields) != 16 {
			return nil, errors.New("incomplete Linux interface counters")
		}
		rx, rxErr := strconv.ParseUint(string(fields[0]), 10, 64)
		tx, txErr := strconv.ParseUint(string(fields[8]), 10, 64)
		if rxErr != nil || txErr != nil {
			return nil, errors.New("invalid Linux interface byte counters")
		}
		devices[string(name)] = counters{rx, tx}
	}
	return devices, scan.Err()
}

func sampleDelta(before, after map[string]counters, window time.Duration, at time.Time) *Sample {
	if before == nil || after == nil || window < time.Millisecond {
		return nil
	}
	var rx, tx uint64
	for name, current := range after {
		if previous, ok := before[name]; ok {
			rx = saturatingAdd(rx, counterDelta(previous.rx, current.rx))
			tx = saturatingAdd(tx, counterDelta(previous.tx, current.tx))
		}
	}
	return &Sample{RXBytesPerSecond: rate(rx, window), TXBytesPerSecond: rate(tx, window), WindowMillis: uint64(window.Milliseconds()), ObservedAt: at}
}

func counterDelta(previous, current uint64) uint64 {
	if current < previous {
		return current
	}
	return current - previous
}

func saturatingAdd(a, b uint64) uint64 {
	value, carry := bits.Add64(a, b, 0)
	if carry != 0 {
		return math.MaxUint64
	}
	return value
}

func rate(amount uint64, window time.Duration) uint64 {
	hi, lo := bits.Mul64(amount, uint64(time.Second))
	if hi >= uint64(window) {
		return math.MaxUint64
	}
	count, _ := bits.Div64(hi, lo, uint64(window))
	return count
}
