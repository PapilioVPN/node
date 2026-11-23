package services

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

const cpuSampleInterval = time.Second

const (
	cpuUser = iota
	cpuNice
	cpuSystem
	cpuIdle
	cpuIOWait
	cpuIRQ
	cpuSoftIRQ
	cpuSteal
	cpuCounterCount
)

type CPUSample struct {
	BusyPercent  float64
	StealPercent float64
	HasSteal     bool
	Window       time.Duration
	ObservedAt   time.Time
}

// CPUSampler publishes one immutable sample shared by all Host transports.
// Start and Stop are called once in order by the Host lifecycle.
type CPUSampler struct {
	latest atomic.Pointer[CPUSample]
	cancel context.CancelFunc
	done   chan struct{}
}

func NewCPUSampler() *CPUSampler {
	return &CPUSampler{}
}

func (sampler *CPUSampler) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The Fx startup context expires independently of the application lifetime.
	runCtx, cancel := context.WithCancel(context.Background())
	sampler.cancel = cancel
	sampler.done = make(chan struct{})
	go sampler.run(runCtx)
	return nil
}

func (sampler *CPUSampler) Stop(ctx context.Context) error {
	sampler.cancel()
	select {
	case <-sampler.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (sampler *CPUSampler) Latest() (CPUSample, bool) {
	sample := sampler.latest.Load()
	if sample == nil {
		return CPUSample{}, false
	}
	return *sample, true
}

func (sampler *CPUSampler) run(ctx context.Context) {
	defer close(sampler.done)
	defer sampler.latest.Store(nil)

	previous, err := readCPU()
	hasPrevious := err == nil
	ticker := time.NewTicker(cpuSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, err := readCPU()
			var sample *CPUSample
			if err == nil && hasPrevious {
				sample = cpuDelta(previous, current)
			}
			sampler.latest.Store(sample)
			previous, hasPrevious = current, err == nil
		}
	}
}

type cpuReading struct {
	ticks      [cpuCounterCount]uint64
	count      int
	observedAt time.Time
}

func readCPU() (cpuReading, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return cpuReading{}, err
	}
	defer file.Close()
	// Only the first line is needed, regardless of the number of CPUs.
	line, err := bufio.NewReaderSize(file, 512).ReadSlice('\n')
	if err != nil {
		return cpuReading{}, err
	}
	return parseCPU(line, time.Now())
}

func parseCPU(line []byte, observedAt time.Time) (cpuReading, error) {
	fields := bytes.Fields(line)
	if len(fields) <= cpuIdle+1 || !bytes.Equal(fields[0], []byte("cpu")) {
		return cpuReading{}, errors.New("missing aggregate CPU counters")
	}
	reading := cpuReading{count: min(len(fields)-1, cpuCounterCount), observedAt: observedAt}
	for i := range reading.count {
		ticks, err := strconv.ParseUint(string(fields[i+1]), 10, 64)
		if err != nil {
			return cpuReading{}, fmt.Errorf("parse CPU counter %d: %w", i, err)
		}
		reading.ticks[i] = ticks
	}
	return reading, nil
}

func cpuDelta(previous, current cpuReading) *CPUSample {
	window := current.observedAt.Sub(previous.observedAt)
	if current.count != previous.count || window < time.Millisecond {
		return nil
	}
	var delta [cpuCounterCount]float64
	var total float64
	for i := range current.count {
		if current.ticks[i] < previous.ticks[i] {
			return nil
		}
		delta[i] = float64(current.ticks[i] - previous.ticks[i])
		total += delta[i]
	}
	if total == 0 {
		return nil
	}
	// Guest ticks are already included in user and nice. Idle, I/O wait and
	// stolen time contribute to the elapsed CPU time without running local work.
	busy := delta[cpuUser] + delta[cpuNice] + delta[cpuSystem] + delta[cpuIRQ] + delta[cpuSoftIRQ]
	return &CPUSample{
		BusyPercent:  100 * (busy / total),
		StealPercent: 100 * (delta[cpuSteal] / total),
		HasSteal:     current.count > cpuSteal,
		Window:       window,
		ObservedAt:   current.observedAt,
	}
}
