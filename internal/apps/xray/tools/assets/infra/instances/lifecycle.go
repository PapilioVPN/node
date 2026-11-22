// Package instances borrows existing instance runtimes through an exclusive reservation.
package instances

import (
	"context"
	"errors"
	instance "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/services"
	instancedomain "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
)

// Acquire must reserve the existing Instances mutation slot, reject stale
// execution IDs/closed ownership and hold admission through rollback/readiness.
// The returned runtime is borrowed; Assets never creates or closes it. No
// fallback to separate public Stop/Start calls is safe across this reservation.
// Integration must bind this runtime to the managed current/xray path and reject
// source-level asset overrides/downloads that bypass the selected release.
type Acquire func(context.Context, string, string) (instance.Runtime, func(), error)
type Lifecycle struct{ Acquire Acquire }

func (l Lifecycle) Reserve(ctx context.Context, id, executionID string) (services.Session, func(), error) {
	if l.Acquire == nil {
		return nil, nil, domain.ErrUnsupported
	}
	runtime, release, err := l.Acquire(ctx, id, executionID)
	if err != nil {
		switch {
		case errors.Is(err, instancedomain.ErrBusy):
			err = errors.Join(domain.ErrBusy, err)
		case errors.Is(err, instancedomain.ErrConflict), errors.Is(err, instancedomain.ErrExecutionChanged):
			err = errors.Join(domain.ErrConflict, err)
		case errors.Is(err, instancedomain.ErrNotFound):
			err = errors.Join(domain.ErrNotFound, err)
		case errors.Is(err, instancedomain.ErrUnsupported):
			err = errors.Join(domain.ErrUnsupported, err)
		}
		return nil, nil, err
	}
	if runtime == nil || release == nil {
		if release != nil {
			release()
		}
		return nil, nil, domain.ErrUnsupported
	}
	if !runtime.APIConfigured() {
		release()
		return nil, nil, domain.ErrUnsupported
	}
	if runtime.Observe().ExecutionID != executionID {
		release()
		return nil, nil, domain.ErrConflict
	}
	return runtime, release, nil
}
