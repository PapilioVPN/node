// Package shutdown serializes requests to the process shutdown broadcaster.
package shutdown

import (
	"sync"

	"go.uber.org/fx"
)

// Synchronize preserves native shutdown behavior while protecting Fx v1.24.0's
// mutable exit-code field from concurrent resource failure notifications.
func Synchronize(inner fx.Shutdowner) fx.Shutdowner { return &serialized{inner: inner} }

type serialized struct {
	mu    sync.Mutex
	inner fx.Shutdowner
}

func (shutdown *serialized) Shutdown(options ...fx.ShutdownOption) error {
	shutdown.mu.Lock()
	defer shutdown.mu.Unlock()
	return shutdown.inner.Shutdown(options...)
}
