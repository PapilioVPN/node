// Package domain defines host identity observations independently of OS and wire APIs.
package domain

import "time"

// Identity contains the kernel identity visible in the process's UTS namespace.
// A container must join the host UTS namespace to observe the host's identity.
type Identity struct {
	Hostname        string
	OperatingSystem string
	Release         string
	Version         string
	Machine         string
}

// Observation records completion of a fresh identity read and its elapsed time.
type Observation struct {
	Identity   Identity
	ObservedAt time.Time
	Duration   time.Duration
}
