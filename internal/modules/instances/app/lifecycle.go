// Package app owns instance lifecycle operations independently of engine adapters.
package app

import (
	"context"

	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

// Runtime is the lifecycle capability consumed by one bound instance.
// Start admits execution; it does not imply readiness. A started runtime outlives
// its Start request and belongs to its resource owner until stopped or exited.
// Implementations serialize conflicting mutations and preserve native causes.
type Runtime interface {
	Start(context.Context) error
	Stop(context.Context) error
	Observe() (domain.State, error)
}

// Lifecycle binds one stable instance identity to its injected runtime.
type Lifecycle struct {
	instance domain.Instance
	runtime  Runtime
}

func NewLifecycle(instance domain.Instance, runtime Runtime) *Lifecycle {
	return &Lifecycle{instance: instance, runtime: runtime}
}

// Start returns the observed outcome even when an operation reports an error.
func (s *Lifecycle) Start(ctx context.Context) (domain.Status, error) {
	err := s.runtime.Start(ctx)
	state, failure := s.runtime.Observe()
	return domain.Status{Instance: s.instance, State: state, Failure: failure}, err
}

// Stop preserves partial outcomes and the runtime's native cancellation/error cause.
func (s *Lifecycle) Stop(ctx context.Context) (domain.Status, error) {
	err := s.runtime.Stop(ctx)
	state, failure := s.runtime.Observe()
	return domain.Status{Instance: s.instance, State: state, Failure: failure}, err
}

// Status observes lifecycle state without starting, restarting or probing traffic.
func (s *Lifecycle) Status(ctx context.Context) (domain.Status, error) {
	if err := ctx.Err(); err != nil {
		return domain.Status{}, err
	}
	state, failure := s.runtime.Observe()
	return domain.Status{Instance: s.instance, State: state, Failure: failure}, nil
}
