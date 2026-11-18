package settings

import "time"

// NATSSubscription bounds one capability-owned request/reply endpoint.
type NATSSubscription struct {
	Subject         string        `yaml:"subject"`
	PendingMessages int           `yaml:"pending_messages"`
	PendingBytes    int           `yaml:"pending_bytes"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}
