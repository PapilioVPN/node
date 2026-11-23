package cpu

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"time"
)

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

type Sample struct {
	BusyPercent  float64
	StealPercent float64
	HasSteal     bool
	Window       time.Duration
	ObservedAt   time.Time
}

type cpuReading struct {
	ticks      [cpuCounterCount]uint64
	count      int
	observedAt time.Time
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

func cpuDelta(previous, current cpuReading) *Sample {
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
	return &Sample{
		BusyPercent:  100 * (busy / total),
		StealPercent: 100 * (delta[cpuSteal] / total),
		HasSteal:     current.count > cpuSteal,
		Window:       window,
		ObservedAt:   current.observedAt,
	}
}
