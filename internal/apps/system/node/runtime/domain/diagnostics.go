package domain

import "time"

type Listener struct{ Ready bool }
type Broker struct {
	State          string
	ProbeSucceeded bool
	RoundTrip      time.Duration
	ErrorCode      string
	Cause          error
	InMessages     uint64
	OutMessages    uint64
	InBytes        uint64
	OutBytes       uint64
	Reconnects     uint64
}
type Diagnostics struct {
	HTTP *Listener
	GRPC *Listener
	NATS *Broker
}
type DiagnosticObservation struct {
	Diagnostics Diagnostics
	Healthy     bool
	ObservedAt  time.Time
	Duration    time.Duration
}
