// Package instances owns the lifecycle of configured Hysteria server executions.
package lifecycle

import (
	"context"
	"crypto/sha256"
	"time"

	logs "github.com/PapilioVPN/node/internal/engines/hysteria/logs/live"
	stats "github.com/PapilioVPN/node/internal/engines/hysteria/stats/observations"
)

type State uint8

const (
	Stopped State = iota + 1
	Starting
	Ready
	Stopping
	Failed
)

type Action uint8

const (
	Started Action = iota + 1
	Heartbeat
	Restarted
	Recovered
)

type Status struct {
	ID      string
	State   State
	Version string
	Action  Action
	Digest  *[sha256.Size]byte
}

type ConfigRequest struct {
	ID string
	// Nil selects the configured file. A non-nil empty source is invalid.
	Source []byte
}

type Backend interface {
	Source(context.Context, []byte) ([]byte, error)
	Prepare(context.Context, []byte) (Candidate, error)
}

type Candidate interface {
	Launch(context.Context, uint64) (Run, error)
}

type Run interface {
	Version() string
	Context() context.Context
	Stats() stats.Native
	Logs() *logs.Journal
	Done() <-chan struct{}
	Stop(context.Context)
}

type Execution struct {
	Number  uint64
	ReadyAt time.Time
	Run     Run
}
