// Package domain defines system-wide host processor observations.
package domain

import "time"

// Averages counts runnable and uninterruptible tasks over each averaging window.
// These are system-wide load averages, not CPU percentages or cgroup utilization.
type Averages struct {
	OneMinute      float64
	FiveMinutes    float64
	FifteenMinutes float64
}

type Observation struct {
	Averages   Averages
	ObservedAt time.Time
	Duration   time.Duration
}
