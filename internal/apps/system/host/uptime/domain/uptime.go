// Package domain defines uptime observations independently of Linux and wire APIs.
package domain

import "time"

// Observation measures elapsed time since boot, including time spent suspended.
// Uptime belongs to the reader's Linux time namespace, not to the Node process.
// ObservedAt is read completion; it is not an authoritative wall-clock boot date.
type Observation struct {
	Uptime     time.Duration
	ObservedAt time.Time
	Duration   time.Duration
}
